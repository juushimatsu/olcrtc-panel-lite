package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/juushimatsu/olcrtc-panel-lite/internal/certificates"
	"github.com/juushimatsu/olcrtc-panel-lite/internal/redact"
)

var bundlePattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

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
	cert, _ := certificates.Ensure(s.cfg.TLSDir, s.cfg.PublicIP)
	writeJSON(w, http.StatusOK, map[string]any{"interface": map[string]any{"theme": theme}, "https": map[string]any{"public_ip": s.cfg.PublicIP, "port": s.cfg.PublicPort, "ca_fingerprint": cert.CAFingerprint, "server_fingerprint": cert.ServerFingerprint, "hsts": s.cfg.HSTS}, "instances": map[string]any{"maximum": s.cfg.MaxInstances}, "yandex": map[string]any{"enabled": yandexEnabled == "true", "base_path": yandexPath, "token_set": tokenErr == nil}, "wb": wbStatus(), "updates": map[string]any{"panel_version": s.cfg.PanelVersion, "upstream_sha": s.cfg.UpstreamSHA, "configured": s.cfg.ReleaseManifestURL != ""}})
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
	writeJSON(w, http.StatusOK, s.operations.get("wb"))
}

func (s *Server) handleWBSettingsGet(w http.ResponseWriter, r *http.Request) {
	mode, _ := s.store.SettingOrDefault(r.Context(), "wb_proxy_mode", "direct")
	address, _ := s.store.SettingOrDefault(r.Context(), "wb_proxy_address", "")
	_, _, passErr := s.store.Setting(r.Context(), "wb_proxy_password")
	_, _, tokenErr := s.store.Setting(r.Context(), "wb_token")
	exp, _ := s.store.SettingOrDefault(r.Context(), "wb_token_exp", "")
	writeJSON(w, http.StatusOK, map[string]any{"proxy_mode": mode, "proxy_address": address, "proxy_password_set": passErr == nil, "token_set": tokenErr == nil, "token_exp": exp, "components": wbStatus()})
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
	if !wbStatus()["installed"].(bool) {
		writeError(w, r, http.StatusUnprocessableEntity, "wb_not_installed", "Сначала установите WB components")
		return
	}
	expires := time.Now().Add(15 * time.Minute)
	if err := s.writeWBJob(r.Context(), expires); err != nil {
		writeError(w, r, http.StatusInternalServerError, "wb_job_failed", "Не удалось подготовить WB job")
		return
	}
	_ = s.store.SetSetting(r.Context(), "wb_session_expires", expires.Format(time.RFC3339), false)
	_ = s.store.SetSetting(r.Context(), "wb_session_extended", "false", false)
	if runtime.GOOS == "linux" {
		_ = exec.CommandContext(r.Context(), "systemctl", "start", "olcrtc-wb-session.service").Run()
	}
	audit(s, r, "wb.session_start", "wb", "session", "success", "")
	writeJSON(w, http.StatusCreated, map[string]any{"active": true, "expires_at": expires, "novnc_url": "/wb/novnc/"})
}

func (s *Server) handleWBSessionGet(w http.ResponseWriter, r *http.Request) {
	expires, _ := s.store.SettingOrDefault(r.Context(), "wb_session_expires", "")
	extended, _ := s.store.SettingOrDefault(r.Context(), "wb_session_extended", "false")
	active := false
	if t, err := time.Parse(time.RFC3339, expires); err == nil {
		active = time.Now().Before(t)
	}
	statePayload := map[string]any{}
	if b, err := os.ReadFile("/var/lib/olcrtc-wb/state.json"); err == nil {
		_ = json.Unmarshal(b, &statePayload)
		if token, ok := statePayload["token"].(string); ok && token != "" {
			encrypted, encryptErr := s.secrets.Encrypt(token)
			if encryptErr == nil {
				_ = s.store.SetSetting(r.Context(), "wb_token", encrypted, true)
				statePayload["applied"] = s.instances.UpdateWBToken(r.Context(), token)
			}
			delete(statePayload, "token")
			if clean, marshalErr := json.Marshal(statePayload); marshalErr == nil {
				_ = writePrivateFile("/var/lib/olcrtc-wb/state.json", clean)
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"active": active, "expires_at": expires, "extended": extended == "true", "novnc_url": "/wb/novnc/", "state": statePayload})
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
	_ = s.store.SetSetting(r.Context(), "wb_session_expires", expires.Format(time.RFC3339), false)
	_ = s.store.SetSetting(r.Context(), "wb_session_extended", "true", false)
	writeJSON(w, http.StatusOK, map[string]any{"active": true, "expires_at": expires, "extended": true})
}

func (s *Server) handleWBSessionStop(w http.ResponseWriter, r *http.Request) {
	_ = s.store.DeleteSetting(r.Context(), "wb_session_expires")
	_ = s.store.DeleteSetting(r.Context(), "wb_session_extended")
	if runtime.GOOS == "linux" {
		_ = exec.CommandContext(r.Context(), "systemctl", "stop", "olcrtc-wb-session.service").Run()
	}
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
	encrypted, err := s.secrets.Encrypt(token)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "secret_encrypt_failed", "Не удалось сохранить token")
		return
	}
	_ = s.store.SetSetting(r.Context(), "wb_token", encrypted, true)
	if exp, ok := jwtExpiration(token); ok {
		_ = s.store.SetSetting(r.Context(), "wb_token_exp", exp.Format(time.RFC3339), false)
	}
	result := s.instances.UpdateWBToken(r.Context(), token)
	audit(s, r, "wb.token_refresh", "wb", "token", "success", "configs updated without subscription rewrite")
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

func (s *Server) handleUpdatesReleases(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"current": map[string]string{"panel_version": s.cfg.PanelVersion, "upstream_sha": s.cfg.UpstreamSHA}, "rollback_available": fileExists("/var/lib/olcrtc-panel/releases/previous")})
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
	writeJSON(w, http.StatusOK, s.operations.get("update"))
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

func (s *Server) writeWBJob(ctx context.Context, expires time.Time) error {
	if err := os.MkdirAll("/var/lib/olcrtc-wb/profile", 0o700); err != nil {
		return err
	}
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
	job := map[string]any{"action": "create", "home_url": "https://stream.wb.ru", "profile_dir": "/var/lib/olcrtc-wb/profile", "state_file": "/var/lib/olcrtc-wb/state.json", "control_file": "/var/lib/olcrtc-wb/control.json", "deadline_unix": expires.Unix(), "proxy": proxy}
	b, err := json.Marshal(job)
	if err != nil {
		return err
	}
	if err := writePrivateFile("/var/lib/olcrtc-wb/job.json", b); err != nil {
		return err
	}
	control, _ := json.Marshal(map[string]int64{"deadline_unix": expires.Unix()})
	return writePrivateFile("/var/lib/olcrtc-wb/control.json", control)
}

func writePrivateFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if account, err := user.Lookup("olcrtc-wb"); err == nil {
		uid, _ := strconv.Atoi(account.Uid)
		gid, _ := strconv.Atoi(account.Gid)
		_ = os.Chown(tmp, uid, gid)
	}
	return os.Rename(tmp, path)
}
