package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/juushimatsu/olcrtc-panel-lite/internal/model"
)

const autoSetupStateKey = "auto_setup_state"

const maxAutoSetupRoomLength = 512

// AutoSetupState is the durable state returned to the first-run wizard.
// Room IDs are intentionally the only provider data persisted here; tokens
// continue to live in the encrypted settings/instance stores.
type AutoSetupState struct {
	Step             string    `json:"step"`
	Progress         int       `json:"progress"`
	Message          string    `json:"message"`
	CurrentAction    string    `json:"current_action"`
	Error            string    `json:"error,omitempty"`
	CompletedSteps   []string  `json:"completed_steps"`
	CreatedInstances []int64   `json:"created_instances,omitempty"`
	SkipTelemost     bool      `json:"skip_telemost,omitempty"`
	WBRoomIDs        []string  `json:"wb_room_ids,omitempty"`
	TelemostRoomID   string    `json:"telemost_room_id,omitempty"`
	StartedAt        time.Time `json:"started_at,omitempty"`
	UpdatedAt        time.Time `json:"updated_at,omitempty"`
}

type autoSetupStartInput struct {
	SkipTelemost   bool     `json:"skip_telemost"`
	Restart        bool     `json:"restart"`
	Step           string   `json:"step"`
	Progress       int      `json:"progress"`
	CurrentAction  string   `json:"current_action"`
	WBRoomIDs      []string `json:"wb_room_ids"`
	RoomIDs        []string `json:"room_ids"`
	TelemostRoomID string   `json:"telemost_room_id"`
}

type autoSetupCompleteInput struct {
	WBRoomIDs        []string `json:"wb_room_ids"`
	RoomIDs          []string `json:"room_ids"`
	TelemostRoomID   string   `json:"telemost_room_id"`
	SkipTelemost     bool     `json:"skip_telemost"`
	StartInstances   *bool    `json:"start_instances"`
	CreatedInstances []int64  `json:"created_instances"`
}

func defaultAutoSetupState() AutoSetupState {
	now := time.Now().UTC()
	return AutoSetupState{
		Step:           "welcome",
		Message:        "Готово к автоматической настройке",
		CurrentAction:  "Ожидание запуска",
		CompletedSteps: []string{},
		StartedAt:      now,
		UpdatedAt:      now,
	}
}

func (s *Server) readAutoSetupStateLocked(ctx context.Context) (AutoSetupState, error) {
	value, err := s.store.SettingOrDefault(ctx, autoSetupStateKey, "")
	if err != nil {
		return AutoSetupState{}, err
	}
	if strings.TrimSpace(value) == "" {
		return defaultAutoSetupState(), nil
	}
	var state AutoSetupState
	if err := json.Unmarshal([]byte(value), &state); err != nil {
		return AutoSetupState{}, fmt.Errorf("decode auto-setup state: %w", err)
	}
	if state.Step == "" {
		state.Step = "welcome"
	}
	if state.CompletedSteps == nil {
		state.CompletedSteps = []string{}
	}
	state.WBRoomIDs = autoSetupRooms(state.WBRoomIDs)
	state.CreatedInstances = autoSetupInstanceIDs(state.CreatedInstances)
	state.TelemostRoomID = cleanAutoSetupText(state.TelemostRoomID, maxAutoSetupRoomLength)
	if state.StartedAt.IsZero() {
		state.StartedAt = time.Now().UTC()
	}
	return state, nil
}

func (s *Server) saveAutoSetupStateLocked(ctx context.Context, state AutoSetupState) error {
	state.Progress = clampAutoSetupProgress(state.Progress)
	if state.CompletedSteps == nil {
		state.CompletedSteps = []string{}
	}
	state.UpdatedAt = time.Now().UTC()
	payload, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("encode auto-setup state: %w", err)
	}
	return s.store.SetSetting(ctx, autoSetupStateKey, string(payload), false)
}

func (s *Server) readAutoSetupState(ctx context.Context) (AutoSetupState, error) {
	s.autoSetupMu.Lock()
	defer s.autoSetupMu.Unlock()
	return s.readAutoSetupStateLocked(ctx)
}

func (s *Server) saveAutoSetupState(ctx context.Context, state AutoSetupState) error {
	s.autoSetupMu.Lock()
	defer s.autoSetupMu.Unlock()
	return s.saveAutoSetupStateLocked(ctx, state)
}

func (s *Server) updateAutoSetupState(ctx context.Context, update func(*AutoSetupState)) (AutoSetupState, error) {
	s.autoSetupMu.Lock()
	defer s.autoSetupMu.Unlock()
	state, err := s.readAutoSetupStateLocked(ctx)
	if err != nil {
		return AutoSetupState{}, err
	}
	update(&state)
	if err := s.saveAutoSetupStateLocked(ctx, state); err != nil {
		return AutoSetupState{}, err
	}
	return state, nil
}

func clampAutoSetupProgress(value int) int {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}

func markAutoSetupStep(state *AutoSetupState, step string) {
	for _, completed := range state.CompletedSteps {
		if completed == step {
			return
		}
	}
	state.CompletedSteps = append(state.CompletedSteps, step)
}

func validAutoSetupStep(step string) bool {
	switch step {
	case "welcome", "playwright_check", "playwright_install", "wb_auth_prompt", "wb_auth_vnc", "telemost_prompt", "telemost_auth_vnc", "wb_rooms_create", "telemost_room_create", "creating_instances", "starting_instances", "completed", "dismissed", "error":
		return true
	default:
		return false
	}
}

func autoSetupRooms(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	rooms := make([]string, 0, min(len(values), 3))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || len([]rune(value)) > maxAutoSetupRoomLength || strings.ContainsAny(value, "\x00\r\n?<>@#$") {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		rooms = append(rooms, value)
		if len(rooms) == 3 {
			break
		}
	}
	return rooms
}

func cleanAutoSetupText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit < 1 {
		return ""
	}
	result := make([]rune, 0, min(len([]rune(value)), limit))
	for _, r := range value {
		if r == '\x00' || r == '\r' || r == '\n' {
			continue
		}
		result = append(result, r)
		if len(result) == limit {
			break
		}
	}
	return string(result)
}

// shouldShowAutoSetup is deliberately fail-closed: a storage error must not
// block access to an already configured panel or repeatedly show the wizard.
func (s *Server) shouldShowAutoSetup(ctx context.Context) bool {
	forced, forcedErr := s.store.SettingOrDefault(ctx, "auto_setup_forced", "false")
	if forcedErr == nil && strings.EqualFold(strings.TrimSpace(forced), "true") {
		return true
	}
	completed, err := s.store.SettingOrDefault(ctx, "first_run_completed", "false")
	if err != nil || strings.EqualFold(strings.TrimSpace(completed), "true") {
		return false
	}
	count, err := s.store.InstanceCount(ctx)
	if err != nil {
		return false
	}
	if count > 0 {
		// Existing installations are upgrades, not first-run installations.
		_ = s.store.SetSetting(ctx, "first_run_completed", "true", false)
		return false
	}
	return true
}

func (s *Server) handleAutoSetupStatus(w http.ResponseWriter, r *http.Request) {
	state, err := s.readAutoSetupState(r.Context())
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "auto_setup_state_read_failed", "Не удалось прочитать состояние автонастройки")
		return
	}
	count, err := s.store.InstanceCount(r.Context())
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "instances_read_failed", "Не удалось прочитать число инстансов")
		return
	}
	shouldShow := s.shouldShowAutoSetup(r.Context())
	completed, _ := s.store.SettingOrDefault(r.Context(), "first_run_completed", "false")
	writeJSON(w, http.StatusOK, map[string]any{
		"should_show":          shouldShow,
		"first_run_completed":  strings.EqualFold(strings.TrimSpace(completed), "true"),
		"completed":            strings.EqualFold(strings.TrimSpace(completed), "true"),
		"instances_count":      count,
		"instances":            count,
		"automation_supported": runtime.GOOS == "linux" && runtime.GOARCH == "amd64",
		"state":                state,
	})
}

func (s *Server) handleAutoSetupStart(w http.ResponseWriter, r *http.Request) {
	var input autoSetupStartInput
	if err := decodeOptionalJSON(w, r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "Проверьте параметры запуска автонастройки")
		return
	}
	state, err := s.readAutoSetupState(r.Context())
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "auto_setup_state_read_failed", "Не удалось прочитать состояние автонастройки")
		return
	}
	if !input.Restart && state.Progress > 0 && state.Progress < 100 && state.Step != "welcome" {
		changed := false
		if len(input.WBRoomIDs) == 0 {
			input.WBRoomIDs = input.RoomIDs
		}
		if len(input.WBRoomIDs) > 0 {
			state.WBRoomIDs = autoSetupRooms(append(state.WBRoomIDs, input.WBRoomIDs...))
			changed = true
		}
		if strings.TrimSpace(input.TelemostRoomID) != "" {
			state.TelemostRoomID = cleanAutoSetupText(input.TelemostRoomID, maxAutoSetupRoomLength)
			changed = true
		}
		if input.SkipTelemost && !state.SkipTelemost {
			state.SkipTelemost = true
			changed = true
		}
		if validAutoSetupStep(input.Step) && input.Step != state.Step {
			state.Step = input.Step
			if input.Progress > 0 {
				state.Progress = clampAutoSetupProgress(input.Progress)
			}
			if input.CurrentAction != "" {
				state.CurrentAction = cleanAutoSetupText(input.CurrentAction, maxAutoSetupRoomLength)
			}
			changed = true
		}
		if changed {
			if err := s.saveAutoSetupState(r.Context(), state); err != nil {
				writeError(w, r, http.StatusInternalServerError, "auto_setup_state_write_failed", "Не удалось сохранить состояние автонастройки")
				return
			}
		}
		writeJSON(w, http.StatusOK, state)
		return
	}
	if input.Restart || state.Progress >= 100 || state.Step == "dismissed" {
		state = defaultAutoSetupState()
	}
	state.SkipTelemost = input.SkipTelemost
	if len(input.WBRoomIDs) == 0 {
		input.WBRoomIDs = input.RoomIDs
	}
	state.WBRoomIDs = autoSetupRooms(input.WBRoomIDs)
	state.TelemostRoomID = cleanAutoSetupText(input.TelemostRoomID, maxAutoSetupRoomLength)
	state.Step = "playwright_check"
	state.Progress = 5
	state.Message = "Проверка компонентов автоматизации"
	state.CurrentAction = "Проверка Playwright, Chromium и noVNC"
	if err := s.saveAutoSetupState(r.Context(), state); err != nil {
		writeError(w, r, http.StatusInternalServerError, "auto_setup_state_write_failed", "Не удалось сохранить состояние автонастройки")
		return
	}
	if err := s.runAutoSetup(r.Context(), input.SkipTelemost); err != nil {
		state.Error = err.Error()
		state.Step = "error"
		state.Message = "Автонастройка остановлена"
		_ = s.saveAutoSetupState(r.Context(), state)
		writeError(w, r, http.StatusUnprocessableEntity, "auto_setup_start_failed", err.Error())
		return
	}
	state, _ = s.readAutoSetupState(r.Context())
	if state.Step == "playwright_install" && s.cfg.SystemdEnabled && s.operations != nil {
		// Production installs can start the same fixed operation used by the
		// settings page. Development mode leaves the explicit button available.
		_ = s.operations.start("wb", "systemd-run", "--unit=olcrtc-wb-components", "--collect", "--wait", "/usr/lib/olcrtc-panel/wb/install-components.sh")
	}
	audit(s, r, "auto_setup.start", "system", "auto_setup", "started", "")
	state, _ = s.readAutoSetupState(r.Context())
	writeJSON(w, http.StatusAccepted, state)
}

func (s *Server) handleAutoSetupProgress(w http.ResponseWriter, r *http.Request) {
	if err := s.refreshAutoSetupProgress(r.Context()); err != nil {
		writeError(w, r, http.StatusInternalServerError, "auto_setup_progress_failed", "Не удалось обновить состояние автонастройки")
		return
	}
	state, err := s.readAutoSetupState(r.Context())
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "auto_setup_state_read_failed", "Не удалось прочитать состояние автонастройки")
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (s *Server) handleAutoSetupSkipTelemost(w http.ResponseWriter, r *http.Request) {
	state, err := s.updateAutoSetupState(r.Context(), func(state *AutoSetupState) {
		state.SkipTelemost = true
		state.TelemostRoomID = ""
		markAutoSetupStep(state, "telemost_skipped")
		if state.Step == "telemost_prompt" || state.Step == "telemost_auth_vnc" || state.Step == "telemost_room_create" {
			state.Step = "wb_rooms_create"
			state.Progress = 65
			state.Message = "Telemost пропущен. Создайте комнаты WB Stream"
			state.CurrentAction = "Ожидание Room ID WB Stream"
		}
	})
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "auto_setup_state_write_failed", "Не удалось сохранить пропуск Telemost")
		return
	}
	audit(s, r, "auto_setup.skip_telemost", "system", "auto_setup", "success", "")
	writeJSON(w, http.StatusOK, state)
}

func (s *Server) handleAutoSetupComplete(w http.ResponseWriter, r *http.Request) {
	var input autoSetupCompleteInput
	if err := decodeOptionalJSON(w, r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "Проверьте параметры завершения автонастройки")
		return
	}
	state, err := s.readAutoSetupState(r.Context())
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "auto_setup_state_read_failed", "Не удалось прочитать состояние автонастройки")
		return
	}
	if len(input.WBRoomIDs) == 0 {
		input.WBRoomIDs = input.RoomIDs
	}
	if len(input.WBRoomIDs) > 0 {
		state.WBRoomIDs = autoSetupRooms(input.WBRoomIDs)
	}
	// If the frontend sends an explicit empty telemostRoomID and skipTelemost is false,
	// preserve the server-side captured room ID instead of clearing it.
	if strings.TrimSpace(input.TelemostRoomID) != "" {
		state.TelemostRoomID = cleanAutoSetupText(input.TelemostRoomID, maxAutoSetupRoomLength)
	} else if input.SkipTelemost {
		state.TelemostRoomID = ""
	}
	if input.SkipTelemost {
		state.SkipTelemost = true
		markAutoSetupStep(&state, "telemost_skipped")
	}
	if len(input.CreatedInstances) > 0 {
		state.CreatedInstances = autoSetupInstanceIDs(input.CreatedInstances)
	}
	state.Step = "creating_instances"
	state.Progress = 70
	state.Message = "Создание инстансов"
	state.CurrentAction = "Сохранение конфигурации инстансов"
	s.logger.Info("auto-setup complete: saving creating_instances state", "wb_rooms", len(state.WBRoomIDs), "telemost", state.TelemostRoomID != "", "start_instances", input.StartInstances != nil && *input.StartInstances)
	if err := s.saveAutoSetupState(r.Context(), state); err != nil {
		writeError(w, r, http.StatusInternalServerError, "auto_setup_state_write_failed", "Не удалось сохранить состояние автонастройки")
		return
	}
	if len(state.WBRoomIDs) > 0 || (!state.SkipTelemost && state.TelemostRoomID != "") {
		s.logger.Info("auto-setup complete: calling finishAutoSetup")
		if err := s.finishAutoSetup(r.Context(), &state, input.StartInstances); err != nil {
			s.logger.Error("auto-setup complete: finishAutoSetup failed", "error", err)
			writeError(w, r, http.StatusUnprocessableEntity, "auto_setup_complete_failed", err.Error())
			return
		}
		s.logger.Info("auto-setup complete: finishAutoSetup succeeded", "step", state.Step, "created", len(state.CreatedInstances))
	} else {
		// An explicit completion without captured rooms is the supported manual
		// fallback. The user can create instances from the regular CRUD screen.
		state.Step = "completed"
		state.Progress = 100
		state.Message = "Автонастройка завершена; инстансы можно создать вручную"
		state.CurrentAction = "Готово"
		if err := s.saveAutoSetupState(r.Context(), state); err != nil {
			writeError(w, r, http.StatusInternalServerError, "auto_setup_state_write_failed", "Не удалось сохранить завершение автонастройки")
			return
		}
		if err := s.store.SetSetting(r.Context(), "first_run_completed", "true", false); err != nil {
			writeError(w, r, http.StatusInternalServerError, "settings_save_failed", "Не удалось сохранить флаг первого запуска")
			return
		}
		_ = s.store.SetSetting(r.Context(), "auto_setup_forced", "false", false)
	}
	audit(s, r, "auto_setup.complete", "system", "auto_setup", "success", "")
	state, _ = s.readAutoSetupState(r.Context())
	writeJSON(w, http.StatusOK, state)
}

func (s *Server) handleAutoSetupDismiss(w http.ResponseWriter, r *http.Request) {
	state, err := s.updateAutoSetupState(r.Context(), func(state *AutoSetupState) {
		state.Step = "dismissed"
		state.Progress = 100
		state.Message = "Автонастройка пропущена"
		state.CurrentAction = "Настройте инстансы вручную в разделе Инстансы"
	})
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "auto_setup_state_write_failed", "Не удалось сохранить отказ от автонастройки")
		return
	}
	if err := s.store.SetSetting(r.Context(), "first_run_completed", "true", false); err != nil {
		writeError(w, r, http.StatusInternalServerError, "settings_save_failed", "Не удалось сохранить флаг первого запуска")
		return
	}
	_ = s.store.SetSetting(r.Context(), "auto_setup_forced", "false", false)
	audit(s, r, "auto_setup.dismiss", "system", "auto_setup", "success", "")
	writeJSON(w, http.StatusOK, state)
}

// handleTriggerAutoSetup is intentionally separate from complete/dismiss so a
// support operator can reset only the wizard marker without deleting instances.
func (s *Server) handleTriggerAutoSetup(w http.ResponseWriter, r *http.Request) {
	if err := s.store.SetSetting(r.Context(), "first_run_completed", "false", false); err != nil {
		writeError(w, r, http.StatusInternalServerError, "settings_save_failed", "Не удалось сбросить флаг первого запуска")
		return
	}
	if err := s.store.DeleteSetting(r.Context(), autoSetupStateKey); err != nil {
		writeError(w, r, http.StatusInternalServerError, "settings_save_failed", "Не удалось сбросить состояние автонастройки")
		return
	}
	if err := s.store.SetSetting(r.Context(), "auto_setup_forced", "true", false); err != nil {
		writeError(w, r, http.StatusInternalServerError, "settings_save_failed", "Не удалось включить принудительный режим автонастройки")
		return
	}
	audit(s, r, "auto_setup.trigger", "system", "auto_setup", "success", "manually triggered from settings")
	w.WriteHeader(http.StatusNoContent)
}

// runAutoSetup advances the non-interactive part of the wizard. Login and room
// creation remain explicit noVNC actions in the browser; once their IDs are
// supplied to /complete, finishAutoSetup performs the durable work.
func (s *Server) runAutoSetup(ctx context.Context, skipTelemost bool) error {
	state, err := s.readAutoSetupState(ctx)
	if err != nil {
		return err
	}
	state.SkipTelemost = skipTelemost
	components := wbStatus()
	installed, _ := components["installed"].(bool)
	supported, _ := components["supported"].(bool)
	// Dev/test mode has no systemd-managed browser runtime. Captured/manual
	// room IDs are still enough to exercise the rest of the wizard there.
	if !installed && !s.cfg.SystemdEnabled && len(state.WBRoomIDs) > 0 {
		installed = true
	}
	if !installed {
		if supported {
			state.Step = "playwright_install"
			state.Progress = 12
			state.Message = "Требуется установка компонентов автоматизации"
			state.CurrentAction = "Запустите установку Playwright в этом окне"
		} else {
			markAutoSetupStep(&state, "playwright_check")
			state.Step = "wb_auth_prompt"
			state.Progress = 22
			state.Message = "Playwright недоступен на этой платформе"
			state.CurrentAction = "Используйте ручной ввод Room ID"
		}
		return s.saveAutoSetupState(ctx, state)
	}
	markAutoSetupStep(&state, "playwright_check")
	markAutoSetupStep(&state, "playwright_install")
	_, _, tokenErr := s.store.Setting(ctx, "wb_token")
	if tokenErr != nil && len(state.WBRoomIDs) == 0 {
		state.Step = "wb_auth_prompt"
		state.Progress = 28
		state.Message = "Войдите в WB Stream через noVNC"
		state.CurrentAction = "Ожидание WB auth token"
		return s.saveAutoSetupState(ctx, state)
	}
	markAutoSetupStep(&state, "wb_auth_prompt")
	if !skipTelemost && state.TelemostRoomID == "" {
		state.Step = "telemost_prompt"
		state.Progress = 42
		state.Message = "Выберите, создавать ли инстанс Telemost"
		state.CurrentAction = "Ожидание выбора пользователя"
		return s.saveAutoSetupState(ctx, state)
	}
	if skipTelemost {
		markAutoSetupStep(&state, "telemost_skipped")
	}
	if len(state.WBRoomIDs) == 0 {
		state.Step = "wb_rooms_create"
		state.Progress = 65
		state.Message = "Создайте комнаты WB Stream через noVNC"
		state.CurrentAction = "Ожидание Room ID WB Stream"
		return s.saveAutoSetupState(ctx, state)
	}
	return s.finishAutoSetup(ctx, &state, nil)
}

func (s *Server) refreshAutoSetupProgress(ctx context.Context) error {
	state, err := s.readAutoSetupState(ctx)
	if err != nil {
		return err
	}
	if state.Step == "playwright_install" {
		progress := operationProgress{State: "idle"}
		if s.operations != nil {
			progress = operationProgressFrom(s.operations.get("wb"), wbComponentsStatePath)
		}
		if wbStatus()["installed"] == true || progress.State == "completed" {
			markAutoSetupStep(&state, "playwright_install")
			state.Step = "wb_auth_prompt"
			state.Progress = 25
			state.Message = "Компоненты установлены. Войдите в WB Stream"
			state.CurrentAction = "Ожидание WB auth token"
		} else if progress.State == "failed" {
			state.Step = "error"
			state.Progress = progress.Percent
			state.Error = progress.Error
			state.Message = "Не удалось установить компоненты автоматизации"
		}
	}
	worker := readWBSessionStateForResponse()
	phase, _ := worker["phase"].(string)
	provider, _ := worker["provider"].(string)
	workerUpdated := workerUnixTime(worker["updated_at"])
	workerFresh := workerUpdated == 0 || state.StartedAt.IsZero() || workerUpdated >= state.StartedAt.Unix()
	if phase == "success" && workerFresh && (state.Step == "wb_auth_prompt" || state.Step == "wb_auth_vnc" || state.Step == "wb_rooms_create" || state.Step == "telemost_prompt" || state.Step == "telemost_auth_vnc" || state.Step == "telemost_room_create") {
		switch provider {
		case automationTeleProvider:
			if room, _ := worker["room_id"].(string); room != "" {
				state.TelemostRoomID = room
				markAutoSetupStep(&state, "telemost_auth_vnc")
				state.Step = "telemost_room_create"
				state.Progress = 60
				state.Message = "Комната Telemost создана"
				state.CurrentAction = "Проверьте Room ID и продолжите"
			}
		case automationWBProvider:
			if room, _ := worker["room_id"].(string); room != "" {
				state.WBRoomIDs = autoSetupRooms(append(state.WBRoomIDs, room))
				markAutoSetupStep(&state, "wb_auth_vnc")
				if len(state.WBRoomIDs) >= 3 {
					if !state.SkipTelemost && state.TelemostRoomID == "" {
						state.Step = "telemost_prompt"
						state.Progress = 80
						state.Message = "Комнаты WB Stream готовы. Выберите Telemost"
						state.CurrentAction = "Ожидание выбора пользователя"
					} else {
						state.Step = "creating_instances"
						state.Progress = 75
						state.Message = "Комнаты WB Stream готовы"
						state.CurrentAction = "Продолжите для создания инстансов"
					}
				} else {
					state.Step = "wb_rooms_create"
					state.Progress = 65
					state.Message = "Создайте оставшиеся комнаты WB Stream"
					state.CurrentAction = "Ожидание Room ID WB Stream"
				}
			}
		}
	}
	return s.saveAutoSetupState(ctx, state)
}

func workerUnixTime(value any) int64 {
	switch typed := value.(type) {
	case float64:
		return int64(typed)
	case int64:
		return typed
	case json.Number:
		parsed, _ := typed.Int64()
		return parsed
	case string:
		parsed, _ := strconv.ParseInt(typed, 10, 64)
		return parsed
	default:
		return 0
	}
}

func (s *Server) finishAutoSetup(ctx context.Context, state *AutoSetupState, startInstances *bool) error {
	if state == nil {
		return errors.New("auto-setup state is nil")
	}
	s.logger.Info("finishAutoSetup: start", "wb_rooms", len(state.WBRoomIDs), "skip_telemost", state.SkipTelemost, "telemost_room", state.TelemostRoomID != "", "start_instances", startInstances != nil && *startInstances)
	state.WBRoomIDs = autoSetupRooms(state.WBRoomIDs)
	items, err := s.instances.List(ctx)
	if err != nil {
		s.logger.Error("finishAutoSetup: failed to list instances", "error", err)
		return err
	}
	byName := make(map[string]model.Instance, len(items))
	for _, item := range items {
		byName[item.Name] = item
	}
	created := append([]int64(nil), state.CreatedInstances...)
	createdSet := make(map[int64]struct{}, len(created))
	for _, id := range created {
		createdSet[id] = struct{}{}
	}
	var failures []string
	for index, room := range state.WBRoomIDs {
		name := autoSetupWBName(index)
		item, ok := byName[name]
		if ok && item.Provider != automationWBProvider {
			failures = append(failures, fmt.Sprintf("%s: имя уже используется другим provider", name))
			continue
		}
		if !ok {
			s.logger.Info("finishAutoSetup: creating WB instance", "name", name, "room", room, "index", index)
			item, err = s.createWBInstanceWithRoom(ctx, name, room, map[bool]int{true: 120, false: 60}[index == 2])
			if err != nil {
				s.logger.Error("finishAutoSetup: failed to create WB instance", "name", name, "error", err)
				failures = append(failures, fmt.Sprintf("%s: %v", name, err))
				continue
			}
			s.logger.Info("finishAutoSetup: created WB instance", "name", name, "id", item.ID)
		}
		if _, ok := createdSet[item.ID]; !ok {
			created = append(created, item.ID)
			createdSet[item.ID] = struct{}{}
		}
	}
	if !state.SkipTelemost && state.TelemostRoomID != "" {
		const name = "# TLM 1🟡"
		item, ok := byName[name]
		if ok && item.Provider != automationTeleProvider {
			failures = append(failures, fmt.Sprintf("%s: имя уже используется другим provider", name))
		} else {
			if !ok {
				s.logger.Info("finishAutoSetup: creating Telemost instance", "room", state.TelemostRoomID)
				item, err = s.createTelemostInstanceWithRoom(ctx, name, state.TelemostRoomID)
				if err != nil {
					s.logger.Error("finishAutoSetup: failed to create Telemost instance", "error", err)
					failures = append(failures, fmt.Sprintf("%s: %v", name, err))
				} else {
					s.logger.Info("finishAutoSetup: created Telemost instance", "id", item.ID)
					created = append(created, item.ID)
				}
			} else if _, exists := createdSet[item.ID]; !exists {
				created = append(created, item.ID)
			}
		}
	}
	if startInstances == nil || *startInstances {
		s.logger.Info("finishAutoSetup: starting instances", "count", len(created))
		for _, id := range created {
			if err := s.instances.Start(ctx, id); err != nil {
				s.logger.Error("finishAutoSetup: failed to start instance", "id", id, "error", err)
				failures = append(failures, fmt.Sprintf("instance %d: %v", id, err))
			}
		}
	} else {
		s.logger.Info("finishAutoSetup: skipping instance startup (start_instances=false)")
	}
	state.CreatedInstances = created
	state.Step = "completed"
	state.Progress = 100
	state.Message = "Автонастройка завершена"
	state.CurrentAction = "Инстансы готовы к работе"
	state.Error = strings.Join(failures, "; ")
	markAutoSetupStep(state, "creating_instances")
	markAutoSetupStep(state, "starting_instances")
	s.logger.Info("finishAutoSetup: saving completed state", "created_count", len(created), "failures", len(failures))
	if err := s.saveAutoSetupState(ctx, *state); err != nil {
		s.logger.Error("finishAutoSetup: failed to save state", "error", err)
		return err
	}
	if err := s.store.SetSetting(ctx, "first_run_completed", "true", false); err != nil {
		s.logger.Error("finishAutoSetup: failed to set first_run_completed", "error", err)
		return err
	}
	s.logger.Info("finishAutoSetup: success", "created", len(created))
	return s.store.SetSetting(ctx, "auto_setup_forced", "false", false)
}

func autoSetupInstanceIDs(values []int64) []int64 {
	seen := make(map[int64]struct{}, len(values))
	result := make([]int64, 0, min(len(values), 1000))
	for _, value := range values {
		if value < 1 {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
		if len(result) == 1000 {
			break
		}
	}
	return result
}

func autoSetupWBName(index int) string {
	switch index {
	case 0:
		return "# WB 1⚡"
	case 1:
		return "# WB 2⚡"
	case 2:
		return "# WB 3🚀"
	default:
		return fmt.Sprintf("# WB %d", index+1)
	}
}

func (s *Server) createWBInstance(ctx context.Context, name string, fps int) (model.Instance, error) {
	state, err := s.readAutoSetupState(ctx)
	if err != nil {
		return model.Instance{}, err
	}
	room := ""
	if len(state.WBRoomIDs) > 0 {
		room = state.WBRoomIDs[0]
	}
	for _, key := range []string{"auto_setup_wb_room_id", "wb_room_id"} {
		if room == "" {
			room, _ = s.store.SettingOrDefault(ctx, key, "")
		}
	}
	if strings.TrimSpace(room) == "" {
		return model.Instance{}, errors.New("WB room ID not set")
	}
	return s.createWBInstanceWithRoom(ctx, name, room, fps)
}

func (s *Server) createWBInstanceWithRoom(ctx context.Context, name, room string, fps int) (model.Instance, error) {
	if fps < 1 {
		fps = 60
	}
	// vp8channel is the supported WB/OLCRTC Client transport; videochannel
	// remains available in the regular form but cannot produce a Client QR.
	return s.instances.Create(ctx, model.Instance{
		Name:      name,
		Provider:  automationWBProvider,
		Transport: "vp8channel",
		RoomID:    strings.TrimSpace(room),
		Options:   model.TransportOptions{VP8FPS: fps, VideoFPS: fps},
	})
}

func (s *Server) createTelemostInstanceWithRoom(ctx context.Context, name, room string) (model.Instance, error) {
	return s.instances.Create(ctx, model.Instance{
		Name:      name,
		Provider:  automationTeleProvider,
		Transport: "vp8channel",
		RoomID:    strings.TrimSpace(room),
	})
}

func decodeOptionalJSON(w http.ResponseWriter, r *http.Request, output any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); errors.Is(err, io.EOF) {
		return nil
	} else if err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain one JSON object")
	}
	return nil
}
