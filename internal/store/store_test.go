package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/openlibrecommunity/olcrtc-panel-lite/internal/model"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestMigrationsAndTrafficIdempotency(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	item, err := st.CreateInstance(ctx, model.Instance{Name: "one", Provider: "jitsi", Transport: "datachannel", RoomID: "https://meet.example/r", DNS: "8.8.8.8:53", ResetPolicy: "never", Options: model.TransportOptions{}, Liveness: model.LivenessOptions{}, Traffic: model.TrafficOptions{}})
	if err != nil {
		t.Fatal(err)
	}
	inserted, err := st.ApplyTrafficEvent(ctx, "cursor-1", item.ID, "session", "target:443", 100, 250, time.Now())
	if err != nil || !inserted {
		t.Fatalf("first insert %v %v", inserted, err)
	}
	inserted, err = st.ApplyTrafficEvent(ctx, "cursor-1", item.ID, "session", "target:443", 100, 250, time.Now())
	if err != nil || inserted {
		t.Fatalf("duplicate insert %v %v", inserted, err)
	}
	counter, err := st.TrafficCounter(ctx, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if counter.UploadBytes != 100 || counter.DownloadBytes != 250 || counter.TotalBytes != 350 {
		t.Fatalf("counter=%#v", counter)
	}
}

func TestLinkedEntriesCascadeButManualRemain(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	instance, err := st.CreateInstance(ctx, model.Instance{Name: "one", Provider: "jitsi", Transport: "datachannel", RoomID: "https://meet.example/r", DNS: "8.8.8.8:53", ResetPolicy: "never"})
	if err != nil {
		t.Fatal(err)
	}
	sub, err := st.CreateSubscription(ctx, model.Subscription{Slug: "abcdefghijklmnop", Name: "sub", RefreshInterval: "10m", Enabled: true}, "key")
	if err != nil {
		t.Fatal(err)
	}
	id := instance.ID
	_, err = st.AddSubscriptionEntry(ctx, model.SubscriptionEntry{SubscriptionID: sub.ID, SourceInstanceID: &id, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.AddSubscriptionEntry(ctx, model.SubscriptionEntry{SubscriptionID: sub.ID, RawURI: "olcrtc://manual", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteInstance(ctx, instance.ID); err != nil {
		t.Fatal(err)
	}
	entries, err := st.SubscriptionEntries(ctx, sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].RawURI != "olcrtc://manual" {
		t.Fatalf("entries=%#v", entries)
	}
}

func TestExpiredSessionIsRejected(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if err := st.CreateAdmin(ctx, "admin", "hash"); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	session := model.Session{IDHash: "id", AdminID: 1, CSRFHash: "csrf", CreatedAt: now.Add(-13 * time.Hour), ExpiresAt: now.Add(-time.Hour), LastSeenAt: now.Add(-time.Hour), IP: "127.0.0.1", UserAgent: "test"}
	if err := st.CreateSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Session(ctx, "id"); !IsNotFound(err) {
		t.Fatalf("expired session err=%v", err)
	}
}

func TestSubscriptionsListWithSingleConnection(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	sub, err := st.CreateSubscription(ctx, model.Subscription{Slug: "abcdefghijklmnop", Name: "sub", RefreshInterval: "10m", Enabled: true}, "key")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddSubscriptionEntry(ctx, model.SubscriptionEntry{SubscriptionID: sub.ID, RawURI: "olcrtc://manual", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	items, err := st.Subscriptions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || len(items[0].Entries) != 1 {
		t.Fatalf("subscriptions=%#v", items)
	}
}
