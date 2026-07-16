package subscription

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openlibrecommunity/olcrtc-panel-lite/internal/instance"
	"github.com/openlibrecommunity/olcrtc-panel-lite/internal/model"
	"github.com/openlibrecommunity/olcrtc-panel-lite/internal/security"
	"github.com/openlibrecommunity/olcrtc-panel-lite/internal/store"
	"github.com/openlibrecommunity/olcrtc-panel-lite/internal/systemd"
)

func TestStandardRendererBindsMetadataToURI(t *testing.T) {
	root := t.TempDir()
	st, err := store.Open(filepath.Join(root, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	key := make([]byte, 32)
	secrets, _ := security.NewSecrets(key)
	manager := instance.NewManager(st, secrets, systemd.New(false), filepath.Join(root, "instances"), filepath.Join(root, "runtime"), 20)
	ctx := context.Background()
	node, err := manager.Create(ctx, model.Instance{Name: "RU-1", Provider: "jitsi", Transport: "datachannel", RoomID: "https://meet.example/room", DNS: "8.8.8.8:53"})
	if err != nil {
		t.Fatal(err)
	}
	sub, err := st.CreateSubscription(ctx, model.Subscription{Slug: "abcdefghijklmnop", Name: "Test", RefreshInterval: "10m", Color: "#0EA58C", Enabled: true}, "")
	if err != nil {
		t.Fatal(err)
	}
	id := node.ID
	_, err = st.AddSubscriptionEntry(ctx, model.SubscriptionEntry{SubscriptionID: sub.ID, SourceInstanceID: &id, Name: "Linked", Comment: "nearest URI", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.AddSubscriptionEntry(ctx, model.SubscriptionEntry{SubscriptionID: sub.ID, RawURI: "olcrtc://manual?datachannel@room#aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa$manual", Name: "Manual", Enabled: true, SortOrder: 2})
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(st, manager, secrets, "https://203.0.113.1:8443")
	body, _, err := service.Standard(ctx, sub.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "#name: Test") || !strings.Contains(body, "##name: Linked") || !strings.Contains(body, "##comment: nearest URI") {
		t.Fatalf("metadata missing:\n%s", body)
	}
	linkedIndex := strings.Index(body, "##name: Linked")
	manualIndex := strings.Index(body, "olcrtc://manual")
	if linkedIndex < 0 || manualIndex < linkedIndex {
		t.Fatalf("metadata binding/order wrong:\n%s", body)
	}
}

func TestExclaveSkipsIncompatibleManual(t *testing.T) {
	root := t.TempDir()
	st, _ := store.Open(filepath.Join(root, "panel.db"))
	defer st.Close()
	secrets, _ := security.NewSecrets(make([]byte, 32))
	manager := instance.NewManager(st, secrets, systemd.New(false), filepath.Join(root, "instances"), filepath.Join(root, "runtime"), 20)
	ctx := context.Background()
	sub, _ := st.CreateSubscription(ctx, model.Subscription{Slug: "abcdefghijklmnop", Name: "Test", RefreshInterval: "10m", Enabled: true}, "")
	_, _ = st.AddSubscriptionEntry(ctx, model.SubscriptionEntry{SubscriptionID: sub.ID, RawURI: "olcrtc://manual", Enabled: true, ExclaveCompatible: false})
	service := NewService(st, manager, secrets, "https://example")
	body, _, err := service.Exclave(ctx, sub.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(body) != "" {
		t.Fatalf("incompatible manual URI published: %q", body)
	}
}
