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
	"net/url"
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
	"github.com/juushimatsu/olcrtc-panel-lite/internal/config"
	"github.com/juushimatsu/olcrtc-panel-lite/internal/redact"
	"github.com/juushimatsu/olcrtc-panel-lite/internal/store"
)

var bundlePattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

const (
	wbNoVNCAddress         = "127.0.0.1:6080"
	wbInstallDir           = "/opt/olcrtc-panel/wb"
	wbRuntimeDir           = "/run/olcrtc-wb"
	automationProfilesDir  = "/var/lib/olcrtc-wb/profiles"
	legacyWBProfileDir     = "/var/lib/olcrtc-wb/profile"
	wbSessionService       = "olcrtc-wb-session.service"
	wbJobPath              = wbRuntimeDir + "/job.json"
	wbStatePath            = wbRuntimeDir + "/state.json"
	wbControlPath          = wbRuntimeDir + "/control.json"
	automationWBProvider   = "wbstream"
	automationTeleProvider = "telemost"
	automationSessionKey   = "wb_session_provider"
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

	// First-run auto-setup wizard. These routes live beside settings because
	// they persist only panel settings and reuse the existing automation API.
	mux.Handle("GET /api/v1/auto-setup/status", s.requireAuth(http.HandlerFunc(s.handleAutoSetupStatus)))
	mux.Handle("POST /api/v1/auto-setup/start", s.requireAuth(http.HandlerFunc(s.handleAutoSetupStart)))
	mux.Handle("GET /api/v1/auto-setup/progress", s.requireAuth(http.HandlerFunc(s.handleAutoSetupProgress)))
	mux.Handle("POST /api/v1/auto-setup/skip-telemost", s.requireAuth(http.HandlerFunc(s.handleAutoSetupSkipTelemost)))
	mux.Handle("POST /api/v1/auto-setup/complete", s.requireAuth(http.HandlerFunc(s.handleAutoSetupComplete)))
	mux.Handle("POST /api/v1/auto-setup/dismiss", s.requireAuth(http.HandlerFunc(s.handleAutoSetupDismiss)))
	mux.Handle("POST /api/v1/settings/trigger-auto-setup", s.requireAuth(http.HandlerFunc(s.handleTriggerAutoSetup)))

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
	mux.Handle("POST /api/v1/wb/profile/reset", s.requireAuth(http.HandlerFunc(s.handleWBProfileReset)))
	mux.Handle("POST /api/v1/wb/token/refresh", s.requireAuth(http.HandlerFunc(s.handleWBTokenRefresh)))

	// Canonical automation API. The /wb routes above remain compatibility aliases.
	mux.Handle("GET /api/v1/automation/components", s.requireAuth(http.HandlerFunc(s.handleWBComponents)))
	mux.Handle("POST /api/v1/automation/components/install", s.requireAuth(http.HandlerFunc(s.handleWBInstall)))
	mux.Handle("POST /api/v1/automation/components/remove", s.requireAuth(http.HandlerFunc(s.handleWBRemove)))
	mux.Handle("GET /api/v1/automation/components/progress", s.requireAuth(http.HandlerFunc(s.handleWBProgress)))
	mux.Handle("GET /api/v1/automation/settings", s.requireAuth(http.HandlerFunc(s.handleWBSettingsGet)))
	mux.Handle("PUT /api/v1/automation/settings", s.requireAuth(http.HandlerFunc(s.handleWBSettingsPut)))
	mux.Handle("POST /api/v1/automation/{provider}/session", s.requireAuth(http.HandlerFunc(s.handleWBSessionStart)))
	mux.Handle("GET /api/v1/automation/{provider}/session", s.requireAuth(http.HandlerFunc(s.handleWBSessionGet)))
	mux.Handle("POST /api/v1/automation/{provider}/session/extend", s.requireAuth(http.HandlerFunc(s.handleWBSessionExtend)))
	mux.Handle("DELETE /api/v1/automation/{provider}/session", s.requireAuth(http.HandlerFunc(s.handleWBSessionStop)))
	mux.Handle("POST /api/v1/automation/{provider}/profile/reset", s.requireAuth(http.HandlerFunc(s.handleWBProfileReset)))
	mux.Handle("POST /api/v1/automation/wbstream/token/refresh", s.requireAuth(http.HandlerFunc(s.handleWBTokenRefresh)))

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
	publicCfg := s.configuredPublicSettings(r.Context())
	restartRequired := publicCfg.PublicOrigin != s.cfg.PublicOrigin || publicCfg.PanelPath != s.cfg.PanelPath || publicCfg.SubscriptionPath != s.cfg.SubscriptionPath
	autoState, _ := s.readAutoSetupState(r.Context())
	autoShouldShow := s.shouldShowAutoSetup(r.Context())
	firstRun, _ := s.store.SettingOrDefault(r.Context(), "first_run_completed", "false")
	writeJSON(w, http.StatusOK, map[string]any{"interface": map[string]any{"theme": theme}, "https": map[string]any{"public_ip": s.cfg.PublicIP, "port": s.cfg.PublicPort, "public_origin": publicCfg.PublicOrigin, "panel_path": publicCfg.PanelPath, "subscription_path": publicCfg.SubscriptionPath, "panel_url": publicCfg.PublicPanelURL(), "subscription_url": publicCfg.PublicSubscriptionBaseURL(), "active_panel_url": s.cfg.PublicPanelURL(), "active_subscription_url": s.cfg.PublicSubscriptionBaseURL(), "restart_required": restartRequired, "ca_fingerprint": cert.CAFingerprint, "server_fingerprint": cert.ServerFingerprint, "hsts": s.cfg.HSTS}, "instances": map[string]any{"maximum": s.cfg.MaxInstances}, "yandex": map[string]any{"enabled": yandexEnabled == "true", "base_path": yandexPath, "token_set": tokenErr == nil}, "wb": wb, "updates": map[string]any{"panel_version": s.cfg.PanelVersion, "upstream_sha": s.cfg.UpstreamSHA, "configured": s.cfg.ReleaseManifestURL != ""}, "auto_setup": map[string]any{"should_show": autoShouldShow, "first_run_completed": strings.EqualFold(strings.TrimSpace(firstRun), "true"), "state": autoState}})
}

func (s *Server) configuredPublicSettings(ctx context.Context) config.Config {
	cfg := s.cfg
	for key, target := range map[string]*string{
		"public_origin":     &cfg.PublicOrigin,
		"panel_path":        &cfg.PanelPath,
		"subscription_path": &cfg.SubscriptionPath,
	} {
		if value, err := s.store.SettingOrDefault(ctx, key, *target); err == nil {
			*target = value
		}
	}
	return cfg
}

func (s *Server) handleSettingsPut(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Theme            string  `json:"theme"`
		MaxInstances     int     `json:"max_instances"`
		PublicIP         string  `json:"public_ip"`
		PublicPort       int     `json:"public_port"`
		PublicOrigin     *string `json:"public_origin"`
		PanelPath        *string `json:"panel_path"`
		SubscriptionPath *string `json:"subscription_path"`
		YandexEnabled    *bool   `json:"yandex_enabled"`
		YandexOAuthToken string  `json:"yandex_oauth_token"`
		ClearYandexToken bool    `json:"clear_yandex_token"`
		YandexBasePath   string  `json:"yandex_base_path"`
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
		s.subscriptions.SetBaseURL(s.cfg.PublicSubscriptionBaseURL())
	}
	if input.PublicOrigin != nil || input.PanelPath != nil || input.SubscriptionPath != nil {
		candidate := s.configuredPublicSettings(r.Context())
		if input.PublicOrigin != nil {
			candidate.PublicOrigin = strings.TrimSpace(*input.PublicOrigin)
		}
		if input.PanelPath != nil {
			candidate.PanelPath = *input.PanelPath
		}
		if input.SubscriptionPath != nil {
			candidate.SubscriptionPath = *input.SubscriptionPath
		}
		if err := candidate.Validate(); err != nil {
			writeError(w, r, http.StatusBadRequest, "invalid_public_url_settings", err.Error())
			return
		}
		if err := s.store.SetSettings(r.Context(), map[string]string{
			"public_origin":     candidate.PublicOrigin,
			"panel_path":        candidate.PanelPath,
			"subscription_path": candidate.SubscriptionPath,
		}); err != nil {
			s.logger.Error("save public URL settings", "error", err)
			writeError(w, r, http.StatusInternalServerError, "settings_save_failed", "Не удалось атомарно сохранить публичные URL")
			return
		}
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
	for _, key := range []string{"wb_token", "wb_token_exp", "wb_proxy_mode", "wb_proxy_address", "wb_proxy_username", "wb_proxy_password", "wb_session_expires", "wb_session_extended", automationSessionKey} {
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
	username, _ := s.store.SettingOrDefault(r.Context(), "wb_proxy_username", "")
	_, _, passErr := s.store.Setting(r.Context(), "wb_proxy_password")
	_, _, tokenErr := s.store.Setting(r.Context(), "wb_token")
	exp, _ := s.store.SettingOrDefault(r.Context(), "wb_token_exp", "")
	writeJSON(w, http.StatusOK, map[string]any{"proxy_mode": mode, "proxy_address": address, "proxy_username": username, "proxy_password_set": passErr == nil, "token_set": tokenErr == nil, "token_exp": exp, "token_expired": tokenExpired(exp), "components": wbStatus()})
}

func (s *Server) handleWBSettingsPut(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ProxyMode          string `json:"proxy_mode"`
		ProxyAddress       string `json:"proxy_address"`
		ProxyUsername      string `json:"proxy_username"`
		ProxyPassword      string `json:"proxy_password"`
		ClearProxyPassword bool   `json:"clear_proxy_password"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "Проверьте proxy")
		return
	}
	proxy, err := normalizeAutomationProxy(input.ProxyMode, input.ProxyAddress, input.ProxyUsername)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_proxy", err.Error())
		return
	}
	if len(input.ProxyPassword) > 1024 || strings.ContainsAny(input.ProxyPassword, "\r\n") {
		writeError(w, r, http.StatusBadRequest, "invalid_proxy_password", "Proxy password слишком длинный или содержит перевод строки")
		return
	}
	if err := s.store.SetSettings(r.Context(), map[string]string{
		"wb_proxy_mode":     proxy.Mode,
		"wb_proxy_address":  proxy.Address,
		"wb_proxy_username": proxy.Username,
	}); err != nil {
		s.logger.Error("save automation proxy settings", "error", err)
		writeError(w, r, http.StatusInternalServerError, "settings_save_failed", "Не удалось сохранить proxy")
		return
	}
	if input.ClearProxyPassword {
		if err := s.store.DeleteSetting(r.Context(), "wb_proxy_password"); err != nil {
			writeError(w, r, http.StatusInternalServerError, "settings_save_failed", "Не удалось удалить proxy password")
			return
		}
	} else if input.ProxyPassword != "" {
		encrypted, err := s.secrets.Encrypt(input.ProxyPassword)
		if err != nil {
			writeError(w, r, http.StatusInternalServerError, "secret_encrypt_failed", "Не удалось сохранить proxy password")
			return
		}
		if err := s.store.SetSetting(r.Context(), "wb_proxy_password", encrypted, true); err != nil {
			writeError(w, r, http.StatusInternalServerError, "settings_save_failed", "Не удалось сохранить proxy password")
			return
		}
	}
	audit(s, r, "wb.settings_update", "wb", "settings", "success", "")
	s.handleWBSettingsGet(w, r)
}

func (s *Server) handleWBSessionStart(w http.ResponseWriter, r *http.Request) {
	wbSessionStateMu.Lock()
	defer wbSessionStateMu.Unlock()
	expires := time.Now().Add(15 * time.Minute)
	input := struct {
		Action   string `json:"action"`
		Provider string `json:"provider"`
	}{Action: "create", Provider: automationWBProvider}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&input); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "Укажите action create или refresh")
		return
	}
	if provider := r.PathValue("provider"); provider != "" {
		input.Provider = provider
	}
	if input.Action != "create" && input.Action != "refresh" {
		writeError(w, r, http.StatusBadRequest, "invalid_action", "Action должен быть create или refresh")
		return
	}
	if !validAutomationProvider(input.Provider) {
		writeError(w, r, http.StatusBadRequest, "invalid_provider", "Provider автоматизации должен быть wbstream или telemost")
		return
	}
	if input.Provider == automationTeleProvider && input.Action != "create" {
		writeError(w, r, http.StatusBadRequest, "invalid_action", "Telemost поддерживает только создание комнаты")
		return
	}
	if runtime.GOOS != "linux" || !wbStatus()["installed"].(bool) {
		writeError(w, r, http.StatusUnprocessableEntity, "wb_not_installed", "Компоненты автоматизации установлены не полностью. Переустановите их в настройках")
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
	if err := prepareAutomationProfile(input.Provider); err != nil {
		s.logger.Error("prepare automation profile", "provider", input.Provider, "error", err)
		writeError(w, r, http.StatusInternalServerError, "wb_profile_failed", "Не удалось подготовить постоянный Chromium profile")
		return
	}
	if err := ensureWBRuntimeDir(); err != nil {
		s.logger.Error("prepare WB runtime", "error", err)
		writeError(w, r, http.StatusInternalServerError, "wb_runtime_failed", "Не удалось подготовить WB runtime directory")
		return
	}
	if err := s.writeWBJob(r.Context(), expires, input.Action, input.Provider); err != nil {
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
	_ = s.store.SetSetting(r.Context(), automationSessionKey, input.Provider, false)
	s.startWBSessionMonitor()
	audit(s, r, "wb.session_start", "wb", "session", "success", "provider="+input.Provider+" action="+input.Action)
	writeJSON(w, http.StatusCreated, map[string]any{"active": true, "action": input.Action, "provider": input.Provider, "expires_at": expires, "novnc_url": automationNoVNCURL(s.cfg.PanelPath)})
}

func (s *Server) handleWBSessionGet(w http.ResponseWriter, r *http.Request) {
	expires, _ := s.store.SettingOrDefault(r.Context(), "wb_session_expires", "")
	extended, _ := s.store.SettingOrDefault(r.Context(), "wb_session_extended", "false")
	provider := s.currentAutomationProvider(r.Context())
	requestedProvider := automationProviderFromRequest(r, provider)
	if !validAutomationProvider(requestedProvider) {
		writeError(w, r, http.StatusBadRequest, "invalid_provider", "Provider автоматизации должен быть wbstream или telemost")
		return
	}
	if r.PathValue("provider") != "" && requestedProvider != provider {
		writeJSON(w, http.StatusOK, map[string]any{"active": false, "provider": requestedProvider, "expires_at": "", "extended": false, "novnc_url": automationNoVNCURL(s.cfg.PanelPath), "state": map[string]any{}})
		return
	}
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
	writeJSON(w, http.StatusOK, map[string]any{"active": active, "provider": requestedProvider, "expires_at": expires, "extended": extended == "true", "novnc_url": automationNoVNCURL(s.cfg.PanelPath), "state": statePayload})
}

func automationNoVNCURL(panelPath string) string {
	query := url.Values{
		"autoconnect": {"true"},
		"resize":      {"scale"},
		"path":        {strings.TrimPrefix(config.JoinURLPath(panelPath, "/wb/novnc/websockify"), "/")},
	}
	return config.JoinURLPath(panelPath, "/wb/novnc/vnc.html") + "?" + query.Encode()
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
	provider, _ := state["provider"].(string)
	return phase == "success" && action == "create" && (provider == "" || provider == "wbstream")
}

func (s *Server) handleWBSessionExtend(w http.ResponseWriter, r *http.Request) {
	provider := automationProviderFromRequest(r, s.currentAutomationProvider(r.Context()))
	if !validAutomationProvider(provider) || provider != s.currentAutomationProvider(r.Context()) {
		writeError(w, r, http.StatusConflict, "wb_session_inactive", "Сессия выбранного provider не активна")
		return
	}
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
	currentProvider := s.currentAutomationProvider(r.Context())
	provider := automationProviderFromRequest(r, currentProvider)
	if !validAutomationProvider(provider) || provider != currentProvider {
		writeError(w, r, http.StatusBadRequest, "invalid_provider", "Provider автоматизации должен быть wbstream или telemost")
		return
	}
	stopWBSessionMonitor()
	_ = s.store.DeleteSetting(r.Context(), "wb_session_expires")
	_ = s.store.DeleteSetting(r.Context(), "wb_session_extended")
	_ = s.store.DeleteSetting(r.Context(), automationSessionKey)
	if runtime.GOOS == "linux" {
		_ = exec.CommandContext(r.Context(), "systemctl", "stop", wbSessionService).Run()
	}
	wbSessionStateMu.Lock()
	cleanupWBWorkerFiles()
	wbSessionStateMu.Unlock()
	audit(s, r, "wb.session_stop", "wb", "session", "success", "")
	w.WriteHeader(http.StatusNoContent)
}

// handleWBProfileReset clears only the selected provider profile.
func (s *Server) handleWBProfileReset(w http.ResponseWriter, r *http.Request) {
	provider := automationProviderFromRequest(r, automationWBProvider)
	if !validAutomationProvider(provider) {
		writeError(w, r, http.StatusBadRequest, "invalid_provider", "Provider автоматизации должен быть wbstream или telemost")
		return
	}
	if s.currentAutomationProvider(r.Context()) == provider {
		stopWBSessionMonitor()
		if runtime.GOOS == "linux" {
			_ = exec.CommandContext(r.Context(), "systemctl", "stop", wbSessionService).Run()
			_ = exec.CommandContext(r.Context(), "systemctl", "reset-failed", wbSessionService).Run()
		}
		wbSessionStateMu.Lock()
		cleanupWBWorkerFiles()
		wbSessionStateMu.Unlock()
		_ = s.store.DeleteSetting(r.Context(), "wb_session_expires")
		_ = s.store.DeleteSetting(r.Context(), "wb_session_extended")
		_ = s.store.DeleteSetting(r.Context(), automationSessionKey)
	}
	if runtime.GOOS == "linux" {
		if err := removeAutomationProfile(automationProfilesDir, provider); err != nil {
			writeError(w, r, http.StatusInternalServerError, "wb_profile_reset_failed", "Не удалось очистить Chromium profile: "+err.Error())
			return
		}
	}
	audit(s, r, "automation.profile_reset", "automation", provider, "success", "chromium profile cleared")
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

func (s *Server) writeWBJob(ctx context.Context, expires time.Time, action, provider string) error {
	mode, _ := s.store.SettingOrDefault(ctx, "wb_proxy_mode", "direct")
	address, _ := s.store.SettingOrDefault(ctx, "wb_proxy_address", "")
	username, _ := s.store.SettingOrDefault(ctx, "wb_proxy_username", "")
	proxySettings, err := normalizeAutomationProxy(mode, address, username)
	if err != nil {
		return fmt.Errorf("invalid stored automation proxy: %w", err)
	}
	proxy := map[string]string{}
	if proxySettings.Mode != "direct" {
		proxy["server"] = proxySettings.Mode + "://" + proxySettings.Address
		if proxySettings.Username != "" {
			proxy["username"] = proxySettings.Username
		}
		if encrypted, _, settingErr := s.store.Setting(ctx, "wb_proxy_password"); settingErr == nil {
			password, decryptErr := s.secrets.Decrypt(encrypted)
			if decryptErr != nil {
				return fmt.Errorf("decrypt automation proxy password: %w", decryptErr)
			}
			if password != "" {
				proxy["password"] = password
			}
		} else if !store.IsNotFound(settingErr) {
			return fmt.Errorf("load automation proxy password: %w", settingErr)
		}
	}
	homeURL := "https://stream.wb.ru"
	existingRoomID := ""
	if provider == "telemost" {
		homeURL = "https://telemost.yandex.ru"
	} else if items, err := s.store.Instances(ctx); err == nil {
		for _, item := range items {
			if item.Provider == "wbstream" && item.RoomID != "" {
				existingRoomID = item.RoomID
				break
			}
		}
	}
	job := map[string]any{"action": action, "provider": provider, "home_url": homeURL, "existing_room_id": existingRoomID, "profile_dir": automationProfileDir(provider), "state_file": wbStatePath, "control_file": wbControlPath, "deadline_unix": expires.Unix(), "proxy": proxy}
	if err := writeWBWorkerJSON(wbJobPath, job); err != nil {
		return err
	}
	if err := writeWBWorkerJSON(wbControlPath, map[string]int64{"deadline_unix": expires.Unix()}); err != nil {
		return err
	}
	return writeWBWorkerJSON(wbStatePath, map[string]any{"phase": "queued", "message": "Запуск Chromium...", "percent": 1, "action": action, "provider": provider, "updated_at": time.Now().Unix()})
}

type automationProxySettings struct {
	Mode     string
	Address  string
	Username string
}

func normalizeAutomationProxy(mode, address, username string) (automationProxySettings, error) {
	settings := automationProxySettings{
		Mode:     strings.ToLower(strings.TrimSpace(mode)),
		Address:  strings.TrimSpace(address),
		Username: strings.TrimSpace(username),
	}
	allowed := map[string]bool{"direct": true, "http": true, "https": true, "socks5": true}
	if !allowed[settings.Mode] {
		return automationProxySettings{}, errors.New("неизвестный режим proxy")
	}
	if len(settings.Username) > 256 || strings.ContainsAny(settings.Username, "\r\n") {
		return automationProxySettings{}, errors.New("proxy username слишком длинный или содержит перевод строки")
	}
	if settings.Mode == "direct" {
		return settings, nil
	}
	if settings.Address == "" {
		return automationProxySettings{}, errors.New("укажите proxy в формате host:port")
	}
	if len(settings.Address) > 512 || strings.ContainsAny(settings.Address, " \t\r\n") || strings.Contains(settings.Address, "://") {
		return automationProxySettings{}, errors.New("proxy address должен иметь формат host:port без схемы и пробелов")
	}
	parsed, err := url.Parse(settings.Mode + "://" + settings.Address)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return automationProxySettings{}, errors.New("proxy address должен иметь формат host:port")
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil || port < 1 || port > 65535 {
		return automationProxySettings{}, errors.New("proxy port должен быть в диапазоне 1..65535")
	}
	return settings, nil
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

func prepareAutomationProfile(provider string) error {
	if !validAutomationProvider(provider) {
		return fmt.Errorf("invalid automation provider %q", provider)
	}
	if err := migrateLegacyWBProfile(); err != nil {
		return err
	}
	command := exec.Command("install", "-d", "-m", "0700", "-o", "olcrtc-wb", "-g", "olcrtc-wb", automationProfilesDir, automationProfileDir(provider))
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("prepare automation profile: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func validAutomationProvider(provider string) bool {
	return provider == automationWBProvider || provider == automationTeleProvider
}

func automationProviderFromRequest(r *http.Request, fallback string) string {
	if provider := r.PathValue("provider"); provider != "" {
		return provider
	}
	return fallback
}

func automationProfileDir(provider string) string {
	return filepath.Join(automationProfilesDir, provider)
}

func removeAutomationProfile(root, provider string) error {
	if !validAutomationProvider(provider) {
		return fmt.Errorf("invalid automation provider %q", provider)
	}
	return os.RemoveAll(filepath.Join(root, provider))
}

func migrateLegacyWBProfile() error {
	target := automationProfileDir(automationWBProvider)
	if info, err := os.Stat(target); err == nil && info.IsDir() {
		return nil
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect WB profile: %w", err)
	}
	legacy, err := os.Stat(legacyWBProfileDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect legacy WB profile: %w", err)
	}
	if !legacy.IsDir() {
		return fmt.Errorf("legacy WB profile is not a directory")
	}
	if err := os.MkdirAll(automationProfilesDir, 0o700); err != nil {
		return fmt.Errorf("create automation profiles directory: %w", err)
	}
	if err := os.Rename(legacyWBProfileDir, target); err != nil {
		return fmt.Errorf("migrate legacy WB profile: %w", err)
	}
	return nil
}

func (s *Server) currentAutomationProvider(ctx context.Context) string {
	provider, _ := s.store.SettingOrDefault(ctx, automationSessionKey, automationWBProvider)
	if !validAutomationProvider(provider) {
		return automationWBProvider
	}
	return provider
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
