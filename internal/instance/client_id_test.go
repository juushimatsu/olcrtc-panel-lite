package instance

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/juushimatsu/olcrtc-panel-lite/internal/model"
	"github.com/juushimatsu/olcrtc-panel-lite/internal/security"
	"github.com/juushimatsu/olcrtc-panel-lite/internal/store"
	"github.com/juushimatsu/olcrtc-panel-lite/internal/systemd"
)

func TestRotateClientIDChangesClientURI(t *testing.T) {
	root := t.TempDir()
	st, err := store.Open(filepath.Join(root, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	secrets, _ := security.NewSecrets(make([]byte, 32))
	manager := NewManager(st, secrets, systemd.New(false), filepath.Join(root, "instances"), filepath.Join(root, "runtime"), 20)
	ctx := context.Background()
	created, err := manager.Create(ctx, model.Instance{Name: "Jitsi", Provider: "jitsi", Transport: "datachannel", RoomID: "https://meet.example/room", DNS: "8.8.8.8:53"})
	if err != nil {
		t.Fatal(err)
	}
	beforeURI, err := manager.URI(ctx, created.ID, "client")
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.RotateClientID(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	after, err := manager.Get(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	afterURI, err := manager.URI(ctx, created.ID, "client")
	if err != nil {
		t.Fatal(err)
	}
	if created.ClientID == after.ClientID || beforeURI == afterURI {
		t.Fatalf("client_id rotation did not change client URI: before=%q after=%q", created.ClientID, after.ClientID)
	}
	if _, err := uuid.Parse(after.ClientID); err != nil {
		t.Fatalf("rotated client_id is not UUID: %q", after.ClientID)
	}
}
