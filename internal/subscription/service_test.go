package subscription

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/juushimatsu/olcrtc-panel-lite/internal/instance"
	"github.com/juushimatsu/olcrtc-panel-lite/internal/model"
	"github.com/juushimatsu/olcrtc-panel-lite/internal/security"
	"github.com/juushimatsu/olcrtc-panel-lite/internal/store"
	"github.com/juushimatsu/olcrtc-panel-lite/internal/systemd"
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
	manual := "olcrtc://jitsi@r/https%3A%2F%2Fmeet.example%2Fmanual?k=" + strings.Repeat("a", 64) + "&t=datachannel&c=aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee#Manual"
	_, err = st.AddSubscriptionEntry(ctx, model.SubscriptionEntry{SubscriptionID: sub.ID, RawURI: manual, Name: "Manual", Enabled: true, SortOrder: 2})
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
	manualIndex := strings.Index(body, manual)
	if linkedIndex < 0 || manualIndex < linkedIndex {
		t.Fatalf("metadata binding/order wrong:\n%s", body)
	}
}

func TestOLCBOXRendererUsesStandardURIsAndKeepsClientFeedSeparate(t *testing.T) {
	root := t.TempDir()
	st, err := store.Open(filepath.Join(root, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	secrets, _ := security.NewSecrets(make([]byte, 32))
	manager := instance.NewManager(st, secrets, systemd.New(false), filepath.Join(root, "instances"), filepath.Join(root, "runtime"), 20)
	ctx := context.Background()
	node, err := manager.Create(ctx, model.Instance{Name: "Linked", Provider: "jitsi", Transport: "datachannel", RoomID: "https://meet.example/room", DNS: "8.8.8.8:53"})
	if err != nil {
		t.Fatal(err)
	}
	sub, err := st.CreateSubscription(ctx, model.Subscription{Slug: "abcdefghijklmnop", Name: "Dual", RefreshInterval: "10m", Enabled: true}, "")
	if err != nil {
		t.Fatal(err)
	}
	id := node.ID
	if _, err := st.AddSubscriptionEntry(ctx, model.SubscriptionEntry{SubscriptionID: sub.ID, SourceInstanceID: &id, Name: "Linked", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	key := strings.Repeat("c", 64)
	clientURI := "olcrtc://jitsi@r/https%3A%2F%2Fmeet.example%2Fmanual?k=" + key + "&t=datachannel&c=manual-client"
	olcboxURI := "olcrtc://jitsi?datachannel@https://meet.example/olcbox#" + key + "$Manual OLCBOX"
	if _, err := st.AddSubscriptionEntry(ctx, model.SubscriptionEntry{SubscriptionID: sub.ID, RawURI: clientURI, Name: "Client manual", Enabled: true, SortOrder: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddSubscriptionEntry(ctx, model.SubscriptionEntry{SubscriptionID: sub.ID, RawURI: olcboxURI, Name: "OLCBOX manual", Enabled: true, SortOrder: 3}); err != nil {
		t.Fatal(err)
	}
	service := NewService(st, manager, secrets, "https://example")
	clientFeed, _, err := service.Standard(ctx, sub.Slug)
	if err != nil {
		t.Fatal(err)
	}
	olcboxFeed, _, err := service.OLCBOX(ctx, sub.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(clientFeed, "olcrtc://jitsi@r/") || strings.Contains(clientFeed, olcboxURI) {
		t.Fatalf("unexpected Client feed:\n%s", clientFeed)
	}
	if !strings.Contains(olcboxFeed, "olcrtc://jitsi?datachannel@https://meet.example/room#") || !strings.Contains(olcboxFeed, olcboxURI) || strings.Contains(olcboxFeed, clientURI) {
		t.Fatalf("unexpected OLCBOX feed:\n%s", olcboxFeed)
	}
}

func TestSummaryIncludesOLCBOXOnlyEntries(t *testing.T) {
	root := t.TempDir()
	st, err := store.Open(filepath.Join(root, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	secrets, _ := security.NewSecrets(make([]byte, 32))
	manager := instance.NewManager(st, secrets, systemd.New(false), filepath.Join(root, "instances"), filepath.Join(root, "runtime"), 20)
	ctx := context.Background()
	sub, err := st.CreateSubscription(ctx, model.Subscription{Slug: "abcdefghijklmnop", Name: "OLCBOX only", RefreshInterval: "10m", Enabled: true}, "")
	if err != nil {
		t.Fatal(err)
	}
	used, available := int64(125), int64(875)
	uri := "olcrtc://jitsi?datachannel@https://meet.example/room#" + strings.Repeat("a", 64)
	if _, err := st.AddSubscriptionEntry(ctx, model.SubscriptionEntry{
		SubscriptionID:  sub.ID,
		RawURI:          uri,
		ManualUsed:      &used,
		ManualAvailable: &available,
		Enabled:         true,
	}); err != nil {
		t.Fatal(err)
	}
	service := NewService(st, manager, secrets, "https://example")
	resolved, err := service.Summary(ctx, sub.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.UsedBytes != used || resolved.AvailableBytes == nil || *resolved.AvailableBytes != available {
		t.Fatalf("unexpected summary: used=%d available=%v", resolved.UsedBytes, resolved.AvailableBytes)
	}
	clientFeed, _, err := service.Standard(ctx, sub.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(clientFeed, uri) {
		t.Fatalf("OLCBOX-only URI leaked into Client feed: %s", clientFeed)
	}
}

func TestBundleUsesClientSubscriptionEndpoint(t *testing.T) {
	root := t.TempDir()
	st, _ := store.Open(filepath.Join(root, "panel.db"))
	defer st.Close()
	secrets, _ := security.NewSecrets(make([]byte, 32))
	manager := instance.NewManager(st, secrets, systemd.New(false), filepath.Join(root, "instances"), filepath.Join(root, "runtime"), 20)
	ctx := context.Background()
	sub, _ := st.CreateSubscription(ctx, model.Subscription{Slug: "abcdefghijklmnop", Name: "Test", RefreshInterval: "10m", Enabled: true}, "")
	service := NewService(st, manager, secrets, "https://example")
	payload, err := service.Bundle(ctx, sub.Slug)
	if err != nil {
		t.Fatal(err)
	}
	text := string(payload)
	if !strings.Contains(text, `"type":"olcrtc-sub"`) || !strings.Contains(text, `"u":"https://example/sub/abcdefghijklmnop"`) || !strings.Contains(text, `"uc":false`) {
		t.Fatalf("unexpected bundle: %s", text)
	}
}

func TestDeleteKeepsLocalSubscriptionWhenMirrorCleanupCannotRun(t *testing.T) {
	root := t.TempDir()
	st, _ := store.Open(filepath.Join(root, "panel.db"))
	defer st.Close()
	secrets, _ := security.NewSecrets(make([]byte, 32))
	manager := instance.NewManager(st, secrets, systemd.New(false), filepath.Join(root, "instances"), filepath.Join(root, "runtime"), 20)
	ctx := context.Background()
	sub, err := st.CreateSubscription(ctx, model.Subscription{Slug: "abcdefghijklmnop", Name: "Test", RefreshInterval: "10m", Enabled: true, MirrorEnabled: true, MirrorStatus: "synced", MirrorPublicURL: "https://yadi.sk/d/test"}, "")
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(st, manager, secrets, "https://example")
	if err := service.Delete(ctx, sub.Slug); err == nil {
		t.Fatal("delete succeeded without Yandex credentials")
	}
	if _, err := st.Subscription(ctx, sub.Slug); err != nil {
		t.Fatalf("local subscription was deleted after mirror cleanup failure: %v", err)
	}
}

func TestBundleIncludesEncryptedMirrorBootstrap(t *testing.T) {
	root := t.TempDir()
	st, _ := store.Open(filepath.Join(root, "panel.db"))
	defer st.Close()
	secrets, _ := security.NewSecrets(make([]byte, 32))
	manager := instance.NewManager(st, secrets, systemd.New(false), filepath.Join(root, "instances"), filepath.Join(root, "runtime"), 20)
	service := NewService(st, manager, secrets, "https://203.0.113.10:8443")
	plainKey, encryptedKey, err := service.GenerateMirrorKey()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	sub, err := st.CreateSubscription(ctx, model.Subscription{Slug: "abcdefghijklmnop", Name: "Mirror", RefreshInterval: "10m", Enabled: true, MirrorEnabled: true, MirrorStatus: "synced", MirrorPublicURL: "https://yadi.sk/d/test"}, encryptedKey)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := service.Bundle(ctx, sub.Slug)
	if err != nil {
		t.Fatal(err)
	}
	var bundle struct {
		URL       string `json:"u"`
		MirrorKey string `json:"mk"`
		Mirrors   []struct {
			Type      string `json:"t"`
			URL       string `json:"u"`
			Encrypted bool   `json:"e"`
			Algorithm string `json:"a"`
		} `json:"m"`
		UpdateWhenConnectedOnly bool `json:"uc"`
	}
	if err := json.Unmarshal(payload, &bundle); err != nil {
		t.Fatal(err)
	}
	if bundle.URL != "https://203.0.113.10:8443/sub/abcdefghijklmnop" || bundle.MirrorKey != plainKey || bundle.UpdateWhenConnectedOnly || len(bundle.Mirrors) != 1 || bundle.Mirrors[0].Type != "yandex_disk" || !bundle.Mirrors[0].Encrypted || bundle.Mirrors[0].Algorithm != "AES-256-GCM" {
		t.Fatalf("unexpected mirror bundle: %s", payload)
	}
}
