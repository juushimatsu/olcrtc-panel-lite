package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/juushimatsu/olcrtc-panel-lite/internal/config"
	"github.com/juushimatsu/olcrtc-panel-lite/internal/instance"
	"github.com/juushimatsu/olcrtc-panel-lite/internal/security"
	"github.com/juushimatsu/olcrtc-panel-lite/internal/store"
	"github.com/juushimatsu/olcrtc-panel-lite/internal/subscription"
	"github.com/juushimatsu/olcrtc-panel-lite/internal/systemd"
)

func TestAutoSetupRoomListIsBoundedAndDeduplicated(t *testing.T) {
	got := autoSetupRooms([]string{" room-a ", "room-a", "room-b", "room-c", "room-d", "bad\nroom"})
	if len(got) != 3 || got[0] != "room-a" || got[1] != "room-b" || got[2] != "room-c" {
		t.Fatalf("unexpected room list: %#v", got)
	}
}

func TestShouldShowAutoSetup(t *testing.T) {
	p := newTestPanel(t)
	if _, err := p.store.SettingOrDefault(context.Background(), "first_run_completed", "false"); err != nil {
		t.Fatal(err)
	}
	csrf := loginTestPanel(t, p)
	resp := p.request(t, http.MethodGet, "/api/v1/auto-setup/status", nil, "")
	var status struct {
		ShouldShow bool `json:"should_show"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		resp.Body.Close()
		t.Fatal(err)
	}
	resp.Body.Close()
	if !status.ShouldShow {
		t.Fatal("empty new installation should show auto-setup")
	}

	resp = p.request(t, http.MethodPost, "/api/v1/auto-setup/dismiss", nil, csrf)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("dismiss status=%d", resp.StatusCode)
	}
	resp = p.request(t, http.MethodGet, "/api/v1/auto-setup/status", nil, "")
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	if status.ShouldShow {
		t.Fatal("dismissed auto-setup should not show again")
	}
}

func TestAutoSetupCompleteCreatesAndStartsWBInstances(t *testing.T) {
	p := newTestPanel(t)
	csrf := loginTestPanel(t, p)
	resp := p.request(t, http.MethodPost, "/api/v1/auto-setup/complete", map[string]any{
		"wb_room_ids":     []string{"room-a", "room-b", "room-c"},
		"skip_telemost":   true,
		"start_instances": false,
	}, csrf)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("complete status=%d", resp.StatusCode)
	}
	var state AutoSetupState
	if err := json.NewDecoder(resp.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	if state.Step != "completed" || len(state.CreatedInstances) != 3 {
		t.Fatalf("unexpected completion state: %#v", state)
	}
	items, err := p.store.Instances(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("instance count=%d want 3", len(items))
	}
	if items[2].Provider != "wbstream" || items[2].Transport != "vp8channel" || items[2].Options.VP8FPS != 120 {
		t.Fatalf("unexpected third WB instance: %#v", items[2])
	}
}

func TestAutoSetupTriggerForcesWizardEvenWithExistingInstances(t *testing.T) {
	p := newTestPanel(t)
	csrf := loginTestPanel(t, p)
	resp := p.request(t, http.MethodPost, "/api/v1/instances", map[string]any{
		"name": "existing", "provider": "jitsi", "transport": "datachannel", "room_id": "https://meet.example/room",
	}, csrf)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("instance create status=%d", resp.StatusCode)
	}
	resp = p.request(t, http.MethodGet, "/api/v1/auto-setup/status", nil, "")
	var before map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&before); err != nil {
		resp.Body.Close()
		t.Fatal(err)
	}
	resp.Body.Close()
	if before["should_show"] == true {
		t.Fatal("existing installation unexpectedly shows wizard")
	}
	resp = p.request(t, http.MethodPost, "/api/v1/settings/trigger-auto-setup", nil, csrf)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("trigger status=%d", resp.StatusCode)
	}
	resp = p.request(t, http.MethodGet, "/api/v1/auto-setup/status", nil, "")
	defer resp.Body.Close()
	var after map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&after); err != nil {
		t.Fatal(err)
	}
	if after["should_show"] != true {
		t.Fatal("manual trigger did not force wizard")
	}
}

func TestAutoSetupStartRejectsUnknownDraftStep(t *testing.T) {
	p := newTestPanel(t)
	csrf := loginTestPanel(t, p)
	resp := p.request(t, http.MethodPost, "/api/v1/auto-setup/start", map[string]any{
		"step": "not-a-step", "progress": 50,
	}, csrf)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		t.Fatalf("start status=%d", resp.StatusCode)
	}
	var state AutoSetupState
	if err := json.NewDecoder(resp.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	if state.Step == "not-a-step" {
		t.Fatal("unknown draft step was persisted")
	}
}

func TestCreateWBInstanceUsesPersistedRoomID(t *testing.T) {
	root := t.TempDir()
	st, err := newTestStoreForAutoSetup(root)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	server := newAutoSetupTestServer(st, root)
	if err := st.SetSetting(context.Background(), "auto_setup_wb_room_id", "room-test", false); err != nil {
		t.Fatal(err)
	}
	item, err := server.createWBInstance(context.Background(), "WB test", 120)
	if err != nil {
		t.Fatal(err)
	}
	if item.Provider != "wbstream" || item.Transport != "vp8channel" || item.RoomID != "room-test" {
		t.Fatalf("unexpected WB instance: %#v", item)
	}
	if item.Options.VP8FPS != 120 {
		t.Fatalf("fps=%d want 120", item.Options.VP8FPS)
	}
}

func TestAutoSetupCompleteCanCreateTelemost(t *testing.T) {
	p := newTestPanel(t)
	csrf := loginTestPanel(t, p)
	resp := p.request(t, http.MethodPost, "/api/v1/auto-setup/complete", map[string]any{
		"telemost_room_id": "12345678901234",
		"start_instances":  false,
	}, csrf)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("complete status=%d", resp.StatusCode)
	}
	items, err := p.store.Instances(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Provider != "telemost" || items[0].RoomID != "12345678901234" {
		t.Fatalf("unexpected Telemost instance: %#v", items)
	}
}

func TestAutoSetupCompletePreservesServerCapturedRoomIDs(t *testing.T) {
	p := newTestPanel(t)
	csrf := loginTestPanel(t, p)
	// Simulate server-side captured Room IDs (like from VNC automation)
	resp := p.request(t, http.MethodPost, "/api/v1/auto-setup/start", map[string]any{
		"wb_room_ids": []string{"server-room-1", "server-room-2", "server-room-3"},
		"step":        "wb_rooms_create",
		"progress":    65,
	}, csrf)
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		t.Fatalf("start status=%d", resp.StatusCode)
	}
	// Complete without sending Room IDs (empty fields in frontend)
	resp = p.request(t, http.MethodPost, "/api/v1/auto-setup/complete", map[string]any{
		"skip_telemost":   true,
		"start_instances": false,
	}, csrf)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("complete status=%d", resp.StatusCode)
	}
	var state AutoSetupState
	if err := json.NewDecoder(resp.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	if len(state.CreatedInstances) != 3 {
		t.Fatalf("expected 3 instances from server-captured rooms, got %d", len(state.CreatedInstances))
	}
	items, err := p.store.Instances(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("instance count=%d want 3", len(items))
	}
	if items[0].RoomID != "server-room-1" || items[1].RoomID != "server-room-2" || items[2].RoomID != "server-room-3" {
		t.Fatalf("unexpected room IDs: %s, %s, %s", items[0].RoomID, items[1].RoomID, items[2].RoomID)
	}
}

// Helpers keep the direct unit test independent from the HTTP test fixture.
func newTestStoreForAutoSetup(root string) (*store.Store, error) {
	return store.Open(filepath.Join(root, "panel.db"))
}

func newAutoSetupTestServer(st *store.Store, root string) *Server {
	secrets, _ := security.NewSecrets(make([]byte, 32))
	cfg := config.Dev(root)
	instances := instance.NewManager(st, secrets, systemd.New(false), cfg.InstancesDir, cfg.RuntimeDir, cfg.ReleaseDir, 20)
	subscriptions := subscription.NewServiceAtSubscriptionPath(st, instances, secrets, cfg.PublicSubscriptionBaseURL())
	return &Server{cfg: cfg, store: st, instances: instances, subscriptions: subscriptions, secrets: secrets, operations: newOperationTracker(), logger: slog.Default()}
}
