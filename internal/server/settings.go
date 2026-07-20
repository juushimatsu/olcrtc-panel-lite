package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/juushimatsu/olcrtc-panel-lite/internal/certificates"
	"github.com/juushimatsu/olcrtc-panel-lite/internal/redact"
)

var bundlePattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

const (
	wbNoVNCAddress   = "127.0.0.1:6080"
	wbNoVNCURL       = "/wb/novnc/vnc.html?autoconnect=true&resize=scale&path=wb/novnc/websockify"
	wbInstallDir     = "/opt/olcrtc-panel/wb"
	wbRuntimeDir     = "/run/olcrtc-wb"
	wbProfileDir     = "/var/lib/olcrtc-wb/profile"
	wbSessionService = "olcrtc-wb-session.service"
	wbJobPath        = wbRuntimeDir + "/job.json"
	wbStatePath      = wbRuntimeDir + "/state.json"
	wbControlPath    = wbRuntimeDir + "/control.json"
)

var wbSessionStateMu sync.Mutex

var wbSessionMonitor = struct {
	sync.Mutex
	generation uint64
	cancel     context.CancelFunc
}{}

func (s *Server) routesSettings(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/settings", s.requireAuth(http.HandlerFunc(s.handleSettingsGet)))
	mux.Handle("PUT /api/v1/settings", s.requireAuth(http.HandlerFunc(s.handleSettingsPut)))

	mux.Handle("GET /api/v1/wb/components", s.requireAuth(http.HandlerFunc(s.handleWBComponents)))
	mux.Handle("POST /api/v1/wb/components/install", s.requireAuth(http.HandlerFunc(s.handleWBInstall)))
	mux.Handle("POST /api/v1/wb/components/remove", s.requireAuth(http.HandlerFunc(s.handleWBRemove)))
	mux.Handle("GET /api/v1/wb/components/progress", s.requireAuth(http.HandlerFunc(s.handleWBProgress)))
	mux.Handle("GET /api/v1/wb/settings", s.requireAuth(http.HandlerFunc(s.handleWBSettingsGet)))
	mux.Handle("PUT /api/v1/wb/settings", s.requireAuth(http.HandlerFunc(s.handleWBSettingsPut)))
	mux.Handle("POST /api/v1/wb/session", s.requireAuth(http.HandlerFunc(s.handleWBSessionStart)))
	mux.Handle("GET /api/v1/wb/session", s.requireAuth(http.HandlerFunc(s.handleWBSessionGet)))
	mux.Handle("POST /api/v1/wb/session/extend", s.requireAuth(http.HandlerFunc(s.handleWBSessionExtend)))
	mux.Handle("DELETE /api/v1/wb/session", s.requireAuth(http.HandlerFunc(s.handleWBSessionStop)))
	mux.Handle("POST /api/v1/wb/token/refresh", s.requireAuth(http.HandlerFunc(s.handleWBTokenRefresh)))

	mux.Handle("GET /api/v1/updates/check", s.requireAuth(http.HandlerFunc(s.handleUpdatesCheck)))
	mux.Handle("GET /api/v1/updates/releases", s.requireAuth(http.HandlerFunc(s.handleUpdatesReleases)))
	mux.Handle("POST /api/v1/updates/install", s.requireAuth(http.HandlerFunc(s.handleUpdatesInstall)))
	mux.Handle("GET /api/v1/updates/progress", s.requireAuth(http.HandlerFunc(s.handleUpdatesProgress)))
	mux.Handle("POST /api/v1/updates/rollback", s.requireAuth(http.HandlerFunc(s.handleUpdatesRollback)))
}

func (s *Server) handleSettingsGet(w http.ResponseWriter, r *http.Request) {
	theme, _ := s.store.SettingOrDefault(r.Context(), "theme", "dark")
	yandexEnabled, _ := s.store.SettingOrDefault(r.Context(), "yandex_enabled", "false")
	yandexPath, _ := s.store.SettingOrDefault(r.Context(), "yandex_base_path", "/olcrtc/subscriptions")
	_, _, tokenErr := s.store.Setting(r.Context(), "yandex_oauth_token")
	wb := wbStatus()
	wbTokenExpires, _ := s.store.SettingOrDefault(r.Context(), "wb_token_exp", "")
	_, _, wbTokenErr := s.store.Setting(r.Context(), "wb_token")
	wb["token_set"] = wbTokenErr == nil
	wb["token_expires_at"] = wbTokenExpires
	wb["token_expired"] = tokenExpired(wbTokenExpires)
	cert, _ := certificates.Ensure(s.cfg.TLSDir, s.cfg.PublicIP)
	writeJSON(w, http.StatusOK, map[string]any{"interface": map[string]any{"theme": theme}, "https": map[string]any{"public_ip": s.cfg.PublicIP, "port": s.cfg.PublicPort, "ca_fingerprint": cert.CAFingerprint, "server_fingerprint": cert.ServerFingerprint, "hsts": s.cfg.HSTS}, "instances": map[string]any{"maximum": s.cfg.MaxInstances}, "yandex": map[string]any{"enabled": yandexEnabled == "true", "base_path": yandexPath, "token_set": tokenErr == nil}, "wb": wb, "updates": map[string]any{"panel_version": s.cfg.PanelVersion, "upstream_sha": s.cfg.UpstreamSHA, "configured": s.cfg.ReleaseManifestURL != ""}})
}

func (s *Server) handleSettingsPut(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Theme            string `json:"theme"`
		MaxInstances     int    `json:"max_instances"`
		PublicIP         string `json:"public_ip"`
		PublicPort       int    `json:"public_port"`
		YandexEnabled    *bool  `json:"yandex_enabled"`
		YandexOAuthToken string `json:"yandex_oauth_token"`
		ClearYandexToken bool   `json:"clear_yandex_token"`
		YandexBasePath   string `json:"yandex_base_path"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "Проверьте настройки")
		return
	}
	if input.Theme != "" {
		if input.Theme != "dark" && input.Theme != "light" {
			writeError(w, r, http.StatusBadRequest, "invalid_theme", "Неизвестная тема")
			return
		}
		_ = s.store.SetSetting(r.Context(), "theme", input.Theme, false)
	}
	if input.MaxInstances != 0 {
		if input.MaxInstances < 1 || input.MaxInstances > 1000 {
			writeError(w, r, http.StatusBadRequest, "invalid_instance_limit", "Лимит должен быть от 1 до 1000")
			return
		}
		s.cfg.MaxInstances = input.MaxInstances
		s.instances.SetMaxInstances(input.MaxInstances)
		_ = s.store.SetSetting(r.Context(), "max_instances", strconv.Itoa(input.MaxInstances), false)
	}
	if input.PublicIP != "" {
		ip := net.ParseIP(strings.TrimSpace(input.PublicIP))
		if ip == nil {
			writeError(w, r, http.StatusBadRequest, "invalid_public_ip", "Укажите корректный IP-адрес")
			return
		}
		s.cfg.PublicIP = ip.String()
		_ = s.store.SetSetting(r.Context(), "public_ip", s.cfg.PublicIP, false)
		if _, err := certificates.RegenerateServer(s.cfg.TLSDir, s.cfg.PublicIP); err != nil {
			writeError(w, r, http.StatusInternalServerError, "certificate_regenerate_failed", "IP сохранён, но leaf certificate не обновлён")
			return
		}
	}
	if input.PublicPort != 0 {
		if input.PublicPort < 1 || input.PublicPort > 65535 {
			writeError(w, r, http.StatusBadRequest, "invalid_public_port", "Порт должен быть от 1 до 65535")
			return
		}
		s.cfg.PublicPort = input.PublicPort
		_ = s.store.SetSetting(r.Context(), "public_port", strconv.Itoa(input.PublicPort), false)
	}
	if input.PublicIP != "" || input.PublicPort != 0 {
		s.subscriptions.SetBaseURL(publicBaseURL(s.cfg))
	}
	if input.YandexEnabled != nil {
		_ = s.store.SetSetting(r.Context(), "yandex_enabled", strconv.FormatBool(*input.YandexEnabled), false)
	}
	if input.YandexBasePath != "" {
		if !strings.HasPrefix(input.YandexBasePath, "/") || strings.Contains(input.YandexBasePath, "..") {
			writeError(w, r, http.StatusBadRequest, "invalid_yandex_path", "Путь Yandex должен быть абсолютным и без '..'")
			return
		}
		_ = s.store.SetSetting(r.Context(), "yandex_base_path", input.YandexBasePath, false)
	}
	if input.ClearYandexToken {
		_ = s.store.DeleteSetting(r.Context(), "yandex_oauth_token")
	} else if input.YandexOAuthToken != "" {
		encrypted, err := s.secrets.Encrypt(strings.TrimSpace(input.YandexOAuthToken))
		if err != nil {
			writeError(w, r, http.StatusInternalServerError, "secret_encrypt_failed", "Не удалось сохранить token")
			return
		}
		_ = s.store.SetSetting(r.Context(), "yandex_oauth_token", encrypted, true)
	}
	audit(s, r, "settings.update", "system", "settings", "success", "")
	s.handleSettingsGet(w, r)
}

func (s *Server) handleWBComponents(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, wbStatus())
}

func (s *Server) handleWBInstall(w http.ResponseWriter, r *http.Request) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		writeError(w, r, http.StatusUnprocessableEntity, "wb_unsupported", "WB automation поддерживается только на linux/amd64")
		return
	}
	if err := s.operations.start("wb", "systemd-run", "--unit=olcrtc-wb-components", "--collect", "--wait", "/usr/lib/olcrtc-panel/wb/install-components.sh"); err != nil {
		writeError(w, r, http.StatusConflict, "operation_running", err.Error())
		return
	}
	audit(s, r, "wb.components_install", "wb", "components", "started", "")
	writeJSON(w, http.StatusAccepted, s.operations.get("wb"))
}

func (s *Server) handleWBRemove(w http.ResponseWriter, r *http.Request) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		writeError(w, r, http.StatusUnprocessableEntity, "wb_unsupported", "WB automation поддерживается только на linux/amd64")
		return
	}
	if err := s.operations.start("wb", "systemd-run", "--unit=olcrtc-wb-components", "--collect", "--wait", "/usr/lib/olcrtc-panel/wb/remove-components.sh"); err != nil {
		writeError(w, r, http.StatusConflict, "operation_running", err.Error())
		return
	}
	for _, key := range []string{"wb_token", "wb_token_exp", "wb_proxy_mode", "wb_proxy_address", "wb_proxy_password", "wb_session_expires", "wb_session_extended"} {
		_ = s.store.DeleteSetting(r.Context(), key)
	}
	audit(s, r, "wb.components_remove", "wb", "components", "started", "")
	writeJSON(w, http.StatusAccepted, s.operations.get("wb"))
}

func (s *Server) handleWBProgress(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, operationProgressFrom(s.operations.get("wb"), wbComponentsStatePath))
}

func (s *Server) handleWBSettingsGet(w http.ResponseWriter, r *http.Request) {
	mode, _ := s.store.SettingOrDefault(r.Context(), "wb_proxy_mode", "direct")
	address, _ := s.store.SettingOrDefault(r.Context(), "wb_proxy_address", "")
	_, _, passErr := s.store.Setting(r.Context(), "wb_proxy_password")
	_, _, tokenErr := s.store.Setting(r.Context(), "wb_token")
	exp, _ := s.store.SettingOrDefault(r.Context(), "wb_token_exp", "")
	writeJSON(w, http.StatusOK, map[string]any{"proxy_mode": mode, "proxy_address": address, "proxy_password_set": passErr == nil, "token_set": tokenErr == nil, "token_exp": exp, "token_expired": tokenExpired(exp), "components": wbStatus()})
}

func (s *Server) handleWBSettingsPut(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ProxyMode          string `json:"proxy_mode"`
		ProxyAddress       string `json:"proxy_address"`
		ProxyPassword      string `json:"proxy_password"`
		ClearProxyPassword bool   `json:"clear_proxy_password"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "Проверьте proxy")
		return
	}
	allowed := map[string]bool{"direct": true, "http": true, "https": true, "socks5": true}
	if !allowed[input.ProxyMode] {
		writeError(w, r, http.StatusBadRequest, "invalid_proxy_mode", "Неизвестный режим proxy")
		return
	}
	_ = s.store.SetSetting(r.Context(), "wb_proxy_mode", input.ProxyMode, false)
	_ = s.store.SetSetting(r.Context(), "wb_proxy_address", input.ProxyAddress, false)
	if input.ClearProxyPassword {
		_ = s.store.DeleteSetting(r.Context(), "wb_proxy_password")
	} else if input.ProxyPassword != "" {
		encrypted, _ := s.secrets.Encrypt(input.ProxyPassword)
		_ = s.store.SetSetting(r.Context(), "wb_proxy_password", encrypted, true)
	}
	audit(s, r, "wb.settings_update", "wb", "settings", "success", "")
	s.handleWBSettingsGet(w, r)
}

func (s *Server) handleWBSessionStart(w http.ResponseWriter, r *http.Request) {
	wbSessionStateMu.Lock()
	defer wbSessionStateMu.Unlock()
	if runtime.GOOS != "linux" || !wbStatus()["installed"].(bool) {
		writeError(w, r, http.StatusUnprocessableEntity, "wb_not_installed", "WB components установлены не полностью. Переустановите их в настройках")
		return
	}
	expires := time.Now().Add(15 * time.Minute)
	input := struct {
		Action string `json:"action"`
	}{Action: "create"}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&input); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "Укажите action create или refresh")
		return
	}
	if input.Action != "create" && input.Action != "refresh" {
		writeError(w, r, http.StatusBadRequest, "invalid_action", "Action должен быть create или refresh")
		return
	}
	if current, _ := s.store.SettingOrDefault(r.Context(), "wb_session_expires", ""); current != "" {
		if deadline, err := time.Parse(time.RFC3339, current); err == nil && time.Now().Before(deadline) && exec.CommandContext(r.Context(), "systemctl", "is-active", "--quiet", wbSessionService).Run() == nil {
			writeError(w, r, http.StatusConflict, "wb_session_active", "WB browser session уже активна")
			return
		}
	}
	stopWBSessionMonitor()
	_ = exec.CommandContext(r.Context(), "systemctl", "stop", wbSessionService).Run()
	_ = exec.CommandContext(r.Context(), "systemctl", "reset-failed", wbSessionService).Run()
	cleanupWBWorkerFiles()
	if err := refreshWBAutomationRuntimeAssets(r.Context()); err != nil {
		s.logger.Error("refresh WB automation runtime", "error", err)
		writeError(w, r, http.StatusInternalServerError, "wb_runtime_refresh_failed", "Не удалось обновить WB automation из текущей версии панели")
		return
	}
	if !wbRuntimeReady() {
		writeError(w, r, http.StatusUnprocessableEntity, "wb_not_installed", "WB components установлены не полностью. Переустановите их в настройках")
		return
	}
	if err := prepareWBProfile(); err != nil {
		s.logger.Error("prepare WB profile", "error", err)
		writeError(w, r, http.StatusInternalServerError, "wb_profile_failed", "Не удалось подготовить постоянный Chromium profile")
		return
	}
	if err := ensureWBRuntimeDir(); err != nil {
		s.logger.Error("prepare WB runtime", "error", err)
		writeError(w, r, http.StatusInternalServerError, "wb_runtime_failed", "Не удалось подготовить WB runtime directory")
		return
	}
	if err := s.writeWBJob(r.Context(), expires, input.Action); err != nil {
		s.logger.Error("prepare WB job", "error", err)
		writeError(w, r, http.StatusInternalServerError, "wb_job_failed", "Не удалось подготовить WB job")
		return
	}
	output, err := exec.CommandContext(r.Context(), "systemctl", "start", wbSessionService).CombinedOutput()
	if err == nil {
		err = waitForTCPStable(r.Context(), wbNoVNCAddress, 15*time.Second, time.Second)
	}
	if err == nil {
		err = exec.CommandContext(r.Context(), "systemctl", "is-active", "--quiet", wbSessionService).Run()
	}
	if err != nil {
		statusOutput, _ := exec.CommandContext(r.Context(), "systemctl", "--no-pager", "--full", "status", wbSessionService).CombinedOutput()
		s.logger.Error("wb session failed to start", "error", err, "output", redact.Text(truncate(string(output)+"\n"+string(statusOutput), 8000)))
		audit(s, r, "wb.session_start", "wb", "session", "failed", "service or noVNC did not become ready")
		cleanupWBWorkerFiles()
		writeError(w, r, http.StatusBadGateway, "wb_session_start_failed", "WB browser session не запустилась. Проверьте journalctl для olcrtc-wb-session.service")
		return
	}
	_ = s.store.SetSetting(r.Context(), "wb_session_expires", expires.Format(time.RFC3339), false)
	_ = s.store.SetSetting(r.Context(), "wb_session_extended", "false", false)
	s.startWBSessionMonitor()
	audit(s, r, "wb.session_start", "wb", "session", "success", "action="+input.Action)
	writeJSON(w, http.StatusCreated, map[string]any{"active": true, "action": input.Action, "expires_at": expires, "novnc_url": wbNoVNCURL})
}

func (s *Server) handleWBSessionGet(w http.ResponseWriter, r *http.Request) {
	expires, _ := s.store.SettingOrDefault(r.Context(), "wb_session_expires", "")
	extended, _ := s.store.SettingOrDefault(r.Context(), "wb_session_extended", "false")
	active := false
	if t, err := time.Parse(time.RFC3339, expires); err == nil {
		active = time.Now().Before(t)
	}
	if active && runtime.GOOS == "linux" {
		active = exec.CommandContext(r.Context(), "systemctl", "is-active", "--quiet", wbSessionService).Run() == nil
		if active {
			connection, err := net.DialTimeout("tcp", wbNoVNCAddress, 250*time.Millisecond)
			active = err == nil
			if connection != nil {
				_ = connection.Close()
			}
		}
	}
	statePayload := readWBSessionStateForResponse()
	applied := false
	if !wbSessionMonitorRunning() {
		statePayload, applied = s.consumeWBSessionState(r.Context())
	}
	if applied {
		audit(s, r, "wb.token_playwright", "wb", "token", "success", "token applied automatically by WB session monitor")
	}
	if phase, _ := statePayload["phase"].(string); phase == "applying" {
		active = true
	}
	s.attachWBCreateToken(r.Context(), statePayload)
	writeJSON(w, http.StatusOK, map[string]any{"active": active, "expires_at": expires, "extended": extended == "true", "novnc_url": wbNoVNCURL, "state": statePayload})
}

func (s *Server) attachWBCreateToken(ctx context.Context, state map[string]any) {
	if !shouldExposeWBCreateToken(state) {
		return
	}
	encrypted, _, err := s.store.Setting(ctx, "wb_token")
	if err != nil {
		return
	}
	token, err := s.secrets.Decrypt(encrypted)
	if err != nil || token == "" {
		if err != nil {
			s.logger.Error("decrypt captured WB token for create response", "error", err)
		}
		return
	}
	state["token"] = token
}

func shouldExposeWBCreateToken(state map[string]any) bool {
	phase, _ := state["phase"].(string)
	action, _ := state["action"].(string)
	return phase == "success" && action == "create"
}

func (s *Server) handleWBSessionExtend(w http.ResponseWriter, r *http.Request) {
	extended, _ := s.store.SettingOrDefault(r.Context(), "wb_session_extended", "false")
	if extended == "true" {
		writeError(w, r, http.StatusConflict, "wb_already_extended", "Сессию можно продлить только один раз")
		return
	}
	expiresRaw, _ := s.store.SettingOrDefault(r.Context(), "wb_session_expires", "")
	expires, err := time.Parse(time.RFC3339, expiresRaw)
	if err != nil || time.Now().After(expires) {
		writeError(w, r, http.StatusConflict, "wb_session_inactive", "WB-сессия не активна")
		return
	}
	expires = expires.Add(15 * time.Minute)
	if err := writeWBWorkerJSON(wbControlPath, map[string]int64{"deadline_unix": expires.Unix()}); err != nil {
		writeError(w, r, http.StatusInternalServerError, "wb_extend_failed", "Не удалось продлить deadline WB worker")
		return
	}
	_ = s.store.SetSetting(r.Context(), "wb_session_expires", expires.Format(time.RFC3339), false)
	_ = s.store.SetSetting(r.Context(), "wb_session_extended", "true", false)
	writeJSON(w, http.StatusOK, map[string]any{"active": true, "expires_at": expires, "extended": true})
}

func (s *Server) handleWBSessionStop(w http.ResponseWriter, r *http.Request) {
	stopWBSessionMonitor()
	_ = s.store.DeleteSetting(r.Context(), "wb_session_expires")
	_ = s.store.DeleteSetting(r.Context(), "wb_session_extended")
	if runtime.GOOS == "linux" {
		_ = exec.CommandContext(r.Context(), "systemctl", "stop", wbSessionService).Run()
	}
	wbSessionStateMu.Lock()
	cleanupWBWorkerFiles()
	wbSessionStateMu.Unlock()
	audit(s, r, "wb.session_stop", "wb", "session", "success", "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleWBTokenRefresh(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Token string `json:"token"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "Укажите token")
		return
	}
	token := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(input.Token), "Bearer "))
	if token == "" || strings.ContainsAny(token, "\r\n") {
		writeError(w, r, http.StatusBadRequest, "invalid_token", "Token пуст или содержит перевод строки")
		return
	}
	expiresAt, err := s.saveWBToken(r.Context(), token, nil)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "secret_encrypt_failed", "Не удалось сохранить token")
		return
	}
	result := s.instances.UpdateWBToken(r.Context(), token)
	s.syncWBTokenSubscriptions(r.Context(), result)
	if expiresAt != nil {
		result["token_expires_at"] = expiresAt.Format(time.RFC3339)
		result["token_expired"] = !expiresAt.After(time.Now())
	}
	audit(s, r, "wb.token_refresh", "wb", "token", "success", "configs and linked subscriptions updated best-effort")
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleUpdatesCheck(w http.ResponseWriter, r *http.Request) {
	if s.cfg.ReleaseManifestURL == "" {
		writeJSON(w, http.StatusOK, map[string]any{"configured": false, "current_version": s.cfg.PanelVersion, "upstream_sha": s.cfg.UpstreamSHA})
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, s.cfg.ReleaseManifestURL, nil)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "update_check_failed", "Некорректный manifest URL")
		return
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		writeError(w, r, http.StatusBadGateway, "update_check_failed", "Manifest недоступен")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		writeError(w, r, http.StatusBadGateway, "update_check_failed", "Manifest вернул ошибку")
		return
	}
	var manifest map[string]any
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&manifest) != nil {
		writeError(w, r, http.StatusBadGateway, "invalid_manifest", "Manifest повреждён")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"configured": true, "current_version": s.cfg.PanelVersion, "current_upstream_sha": s.cfg.UpstreamSHA, "manifest": manifest})
}

func (s *Server) handleUpdatesReleases(w http.ResponseWriter, r *http.Request) {
	s.writeUpdatesReleases(w, r)
}

func (s *Server) handleUpdatesInstall(w http.ResponseWriter, r *http.Request) {
	var input struct {
		BundleID string `json:"bundle_id"`
	}
	if decodeJSON(w, r, &input) != nil || !bundlePattern.MatchString(input.BundleID) {
		writeError(w, r, http.StatusBadRequest, "invalid_bundle", "Некорректный bundle ID")
		return
	}
	if err := s.operations.start("update", "systemd-run", "--unit=olcrtc-panel-update", "--collect", "--wait", "/usr/lib/olcrtc-panel/update.sh", "install", input.BundleID); err != nil {
		writeError(w, r, http.StatusConflict, "operation_running", err.Error())
		return
	}
	audit(s, r, "update.install", "release", input.BundleID, "started", "")
	writeJSON(w, http.StatusAccepted, s.operations.get("update"))
}

func (s *Server) handleUpdatesProgress(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, operationProgressFrom(s.operations.get("update"), panelUpdateStatePath))
}

func (s *Server) handleUpdatesRollback(w http.ResponseWriter, r *http.Request) {
	if err := s.operations.start("update", "systemd-run", "--unit=olcrtc-panel-update", "--collect", "--wait", "/usr/lib/olcrtc-panel/update.sh", "rollback"); err != nil {
		writeError(w, r, http.StatusConflict, "operation_running", err.Error())
		return
	}
	audit(s, r, "update.rollback", "release", "previous", "started", "")
	writeJSON(w, http.StatusAccepted, s.operations.get("update"))
}

type operationState struct {
	State      string    `json:"state"`
	StartedAt  time.Time `json:"started_at,omitempty"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
	Error      string    `json:"error,omitempty"`
	Output     string    `json:"output,omitempty"`
}
type operationTracker struct {
	mu     sync.Mutex
	values map[string]operationState
}

func newOperationTracker() *operationTracker {
	return &operationTracker{values: make(map[string]operationState)}
}
func (o *operationTracker) get(kind string) operationState {
	o.mu.Lock()
	defer o.mu.Unlock()
	state, ok := o.values[kind]
	if !ok {
		return operationState{State: "idle"}
	}
	return state
}
func (o *operationTracker) start(kind, command string, args ...string) error {
	o.mu.Lock()
	if state := o.values[kind]; state.State == "running" {
		o.mu.Unlock()
		return errors.New("операция уже выполняется")
	}
	o.values[kind] = operationState{State: "running", StartedAt: time.Now()}
	o.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		output, err := exec.CommandContext(ctx, command, args...).CombinedOutput()
		state := operationState{State: "completed", StartedAt: o.get(kind).StartedAt, FinishedAt: time.Now(), Output: redact.Text(truncate(string(output), 16000))}
		if err != nil {
			state.State = "failed"
			state.Error = err.Error()
		}
		o.mu.Lock()
		o.values[kind] = state
		o.mu.Unlock()
	}()
	return nil
}

func jwtExpiration(token string) (time.Time, bool) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return time.Time{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}, false
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if json.Unmarshal(payload, &claims) != nil || claims.Exp <= 0 {
		return time.Time{}, false
	}
	return time.Unix(claims.Exp, 0), true
}

func tokenExpired(value string) bool {
	expires, err := time.Parse(time.RFC3339, value)
	return err == nil && !expires.After(time.Now())
}

func (s *Server) saveWBToken(ctx context.Context, token string, hintedExpiration any) (*time.Time, error) {
	encrypted, err := s.secrets.Encrypt(token)
	if err != nil {
		return nil, err
	}
	if err := s.store.SetSetting(ctx, "wb_token", encrypted, true); err != nil {
		return nil, err
	}
	expires, ok := jwtExpiration(token)
	if !ok {
		expires, ok = expirationFromWorker(hintedExpiration)
	}
	if !ok {
		_ = s.store.DeleteSetting(ctx, "wb_token_exp")
		return nil, nil
	}
	if err := s.store.SetSetting(ctx, "wb_token_exp", expires.Format(time.RFC3339), false); err != nil {
		return nil, err
	}
	return &expires, nil
}

func expirationFromWorker(value any) (time.Time, bool) {
	var unix int64
	switch typed := value.(type) {
	case float64:
		unix = int64(typed)
	case int64:
		unix = typed
	case json.Number:
		unix, _ = typed.Int64()
	case string:
		if parsed, err := time.Parse(time.RFC3339, typed); err == nil {
			return parsed, true
		}
		unix, _ = strconv.ParseInt(typed, 10, 64)
	}
	if unix <= 0 {
		return time.Time{}, false
	}
	return time.Unix(unix, 0), true
}

func (s *Server) syncWBTokenSubscriptions(ctx context.Context, result map[string]any) {
	updated, _ := result["updated"].([]int64)
	unique := make(map[string]struct{})
	for _, id := range updated {
		slugs, err := s.store.SubscriptionSlugsForInstance(ctx, id)
		if err != nil {
			continue
		}
		for _, slug := range slugs {
			unique[slug] = struct{}{}
		}
	}
	slugs := make([]string, 0, len(unique))
	for slug := range unique {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)
	mirrors := make([]string, 0, len(slugs))
	for _, slug := range slugs {
		if sub, err := s.store.Subscription(ctx, slug); err == nil && sub.MirrorEnabled {
			mirrors = append(mirrors, slug)
		}
	}
	result["subscriptions_updated"] = slugs
	result["mirrors_scheduled"] = mirrors
	s.subscriptionsChanged(ctx, slugs)
}

func (s *Server) writeWBJob(ctx context.Context, expires time.Time, action string) error {
	mode, _ := s.store.SettingOrDefault(ctx, "wb_proxy_mode", "direct")
	address, _ := s.store.SettingOrDefault(ctx, "wb_proxy_address", "")
	password := ""
	if encrypted, _, err := s.store.Setting(ctx, "wb_proxy_password"); err == nil {
		password, _ = s.secrets.Decrypt(encrypted)
	}
	proxy := map[string]string{}
	if mode != "direct" && address != "" {
		proxy["server"] = mode + "://" + address
		proxy["password"] = password
	}
	existingRoomID := ""
	if items, err := s.store.Instances(ctx); err == nil {
		for _, item := range items {
			if item.Provider == "wbstream" && item.RoomID != "" {
				existingRoomID = item.RoomID
				break
			}
		}
	}
	job := map[string]any{"action": action, "home_url": "https://stream.wb.ru", "existing_room_id": existingRoomID, "profile_dir": wbProfileDir, "state_file": wbStatePath, "control_file": wbControlPath, "deadline_unix": expires.Unix(), "proxy": proxy}
	if err := writeWBWorkerJSON(wbJobPath, job); err != nil {
		return err
	}
	if err := writeWBWorkerJSON(wbControlPath, map[string]int64{"deadline_unix": expires.Unix()}); err != nil {
		return err
	}
	return writeWBWorkerJSON(wbStatePath, map[string]any{"phase": "queued", "message": "Запуск Chromium...", "percent": 1, "updated_at": time.Now().Unix()})
}

func refreshWBAutomationRuntimeAssets(ctx context.Context) error {
	_ = exec.CommandContext(ctx, "systemctl", "stop", "olcrtc-wb-runtime-refresh.service").Run()
	_ = exec.CommandContext(ctx, "systemctl", "reset-failed", "olcrtc-wb-runtime-refresh.service").Run()
	output, err := exec.CommandContext(ctx, "systemd-run", "--quiet", "--wait", "--pipe", "--collect",
		"--unit=olcrtc-wb-runtime-refresh", "/usr/local/bin/olcrtc-panel", "assets", "refresh-wb", "--root", "/").CombinedOutput()
	if err != nil {
		return fmt.Errorf("refresh WB automation runtime: %w: %s", err, strings.TrimSpace(string(output)))
	}
	output, err = exec.CommandContext(ctx, "systemctl", "daemon-reload").CombinedOutput()
	if err != nil {
		return fmt.Errorf("reload WB automation service: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func prepareWBProfile() error {
	command := exec.Command("install", "-d", "-m", "0700", "-o", "olcrtc-wb", "-g", "olcrtc-wb", wbProfileDir)
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("prepare WB profile: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func ensureWBRuntimeDir() error {
	command := exec.Command("install", "-d", "-m", "0750", "-o", "olcrtc-wb", "-g", "olcrtc-wb", wbRuntimeDir)
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("prepare WB runtime: %w: %s", err, strings.TrimSpace(string(output)))
	}
	account, err := user.Lookup("olcrtc-wb")
	if err != nil {
		return fmt.Errorf("lookup WB runtime owner: %w", err)
	}
	uid, err := strconv.Atoi(account.Uid)
	if err != nil {
		return fmt.Errorf("parse WB runtime uid: %w", err)
	}
	gid, err := strconv.Atoi(account.Gid)
	if err != nil {
		return fmt.Errorf("parse WB runtime gid: %w", err)
	}
	if err := filepath.Walk(wbRuntimeDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		return os.Chown(path, uid, gid)
	}); err != nil {
		return fmt.Errorf("repair WB runtime ownership: %w", err)
	}
	return nil
}

func (s *Server) startWBSessionMonitor() {
	ctx, cancel := context.WithCancel(context.Background())
	wbSessionMonitor.Lock()
	previous := wbSessionMonitor.cancel
	wbSessionMonitor.generation++
	generation := wbSessionMonitor.generation
	wbSessionMonitor.cancel = cancel
	wbSessionMonitor.Unlock()
	if previous != nil {
		previous()
	}
	go s.monitorWBSession(ctx, generation)
}

func stopWBSessionMonitor() {
	wbSessionMonitor.Lock()
	wbSessionMonitor.generation++
	cancel := wbSessionMonitor.cancel
	wbSessionMonitor.cancel = nil
	wbSessionMonitor.Unlock()
	if cancel != nil {
		cancel()
	}
}

func wbSessionMonitorRunning() bool {
	wbSessionMonitor.Lock()
	defer wbSessionMonitor.Unlock()
	return wbSessionMonitor.cancel != nil
}

func (s *Server) monitorWBSession(ctx context.Context, generation uint64) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	defer func() {
		wbSessionMonitor.Lock()
		current := wbSessionMonitor.generation == generation
		if current {
			wbSessionMonitor.cancel = nil
		}
		wbSessionMonitor.Unlock()
		if current {
			cleanupWBJobFiles()
		}
	}()
	for {
		state, applied := s.consumeWBSessionState(ctx)
		if applied {
			s.logger.Info("WB token captured and applied automatically")
		}
		phase, _ := state["phase"].(string)
		if phase == "success" || phase == "error" {
			return
		}
		expiresRaw, _ := s.store.SettingOrDefault(ctx, "wb_session_expires", "")
		if expires, err := time.Parse(time.RFC3339, expiresRaw); err == nil && !expires.After(time.Now()) {
			_ = exec.CommandContext(context.Background(), "systemctl", "stop", wbSessionService).Run()
			wbSessionStateMu.Lock()
			_ = writeWBWorkerJSON(wbStatePath, map[string]any{
				"phase": "error", "message": "Время авторизации истекло", "percent": 0, "updated_at": time.Now().Unix(),
			})
			wbSessionStateMu.Unlock()
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Server) consumeWBSessionState(ctx context.Context) (map[string]any, bool) {
	wbSessionStateMu.Lock()
	state := map[string]any{}
	data, err := os.ReadFile(wbStatePath)
	if err != nil || json.Unmarshal(data, &state) != nil {
		wbSessionStateMu.Unlock()
		return state, false
	}
	token, _ := state["token"].(string)
	if token == "" {
		wbSessionStateMu.Unlock()
		return state, false
	}
	expiresAt, err := s.saveWBToken(ctx, token, state["token_expires_at"])
	if err != nil {
		state["phase"] = "error"
		state["message"] = "Token получен, но не удалось безопасно сохранить"
		state["percent"] = 0
		delete(state, "token")
		_ = writeWBWorkerJSON(wbStatePath, state)
		wbSessionStateMu.Unlock()
		s.logger.Error("save Playwright WB token", "error", err)
		return state, false
	}
	state["phase"] = "applying"
	state["message"] = "Применение данных WB Stream..."
	state["percent"] = 95
	if expiresAt != nil {
		state["token_expires_at"] = expiresAt.Format(time.RFC3339)
	}
	delete(state, "token")
	if err := writeWBWorkerJSON(wbStatePath, state); err != nil {
		wbSessionStateMu.Unlock()
		s.logger.Error("secure WB worker state before apply", "error", err)
		return state, false
	}
	wbSessionStateMu.Unlock()

	result := s.instances.UpdateWBToken(ctx, token)
	s.syncWBTokenSubscriptions(ctx, result)
	state["phase"] = "success"
	state["message"] = "Данные WB Stream получены и применены"
	state["percent"] = 100
	state["applied"] = result
	wbSessionStateMu.Lock()
	current := map[string]any{}
	data, readErr := os.ReadFile(wbStatePath)
	if readErr != nil || json.Unmarshal(data, &current) != nil || current["phase"] != "applying" {
		wbSessionStateMu.Unlock()
		return state, true
	}
	if err := writeWBWorkerJSON(wbStatePath, state); err != nil {
		s.logger.Error("remove WB token from worker state", "error", err)
	}
	wbSessionStateMu.Unlock()
	return state, true
}

func readWBSessionStateForResponse() map[string]any {
	wbSessionStateMu.Lock()
	defer wbSessionStateMu.Unlock()
	state := map[string]any{}
	data, err := os.ReadFile(wbStatePath)
	if err != nil || json.Unmarshal(data, &state) != nil {
		return state
	}
	return sanitizeWBSessionStateForResponse(state)
}

func sanitizeWBSessionStateForResponse(state map[string]any) map[string]any {
	if token, _ := state["token"].(string); token != "" {
		delete(state, "token")
		state["phase"] = "applying"
		state["message"] = "Применение данных WB Stream..."
		state["percent"] = 95
	}
	return state
}

func cleanupWBJobFiles() {
	_ = os.Remove(wbJobPath)
	_ = os.Remove(wbControlPath)
}

func cleanupWBWorkerFiles() {
	cleanupWBJobFiles()
	_ = os.Remove(wbStatePath)
}

func writeWBWorkerJSON(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return writePrivateFile(path, append(data, '\n'))
}

func wbRuntimeReady() bool {
	for _, path := range []string{
		wbInstallDir + "/node/bin/node",
		wbInstallDir + "/node_modules/playwright/package.json",
		wbInstallDir + "/node_modules/playwright-core/package.json",
		wbInstallDir + "/worker.mjs",
		"/usr/lib/olcrtc-panel/wb/run-session.sh",
		"/usr/lib/olcrtc-panel/wb/worker.mjs",
	} {
		if !fileExists(path) {
			return false
		}
	}
	browsers, _ := filepath.Glob(wbInstallDir + "/browsers/chromium-*/chrome-linux*/chrome")
	return len(browsers) > 0
}

func waitForTCPStable(ctx context.Context, address string, timeout, stableFor time.Duration) error {
	deadline := time.Now().Add(timeout)
	readySince := time.Time{}
	dialer := net.Dialer{Timeout: 250 * time.Millisecond}
	var lastErr error
	for {
		connection, err := dialer.DialContext(ctx, "tcp", address)
		if err == nil {
			_ = connection.Close()
			if readySince.IsZero() {
				readySince = time.Now()
			}
			if time.Since(readySince) >= stableFor {
				return nil
			}
		} else {
			lastErr = err
			readySince = time.Time{}
		}
		if time.Now().After(deadline) {
			if lastErr != nil {
				return lastErr
			}
			return errors.New("TCP endpoint did not remain ready")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func writePrivateFile(path string, data []byte) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return err
	}
	account, err := user.Lookup("olcrtc-wb")
	if err != nil {
		return err
	}
	uid, err := strconv.Atoi(account.Uid)
	if err != nil {
		return err
	}
	gid, err := strconv.Atoi(account.Gid)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(directory, ".wb-worker-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chown(tmpPath, uid, gid); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
