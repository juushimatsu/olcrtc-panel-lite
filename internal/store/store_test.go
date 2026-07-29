package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/juushimatsu/olcrtc-panel-lite/internal/model"
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

func TestSubscriptionRevisionIsMonotonicAndMirrorStatusDoesNotChangeIt(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	sub, err := st.CreateSubscription(ctx, model.Subscription{
		Slug: "monotonicrevision", Name: "sub", RefreshInterval: "10m", Enabled: true,
	}, "key")
	if err != nil {
		t.Fatal(err)
	}

	future := time.Now().Add(time.Hour).Truncate(time.Second)
	if _, err := st.db.ExecContext(ctx, `UPDATE subscriptions SET updated_at=? WHERE id=?`, formatTime(future), sub.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchSubscriptions(ctx, []string{sub.Slug}); err != nil {
		t.Fatal(err)
	}
	touched, err := st.Subscription(ctx, sub.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := touched.UpdatedAt.Unix(), future.Unix()+1; got != want {
		t.Fatalf("touched revision=%d want=%d", got, want)
	}

	if err := st.SetSubscriptionMirror(ctx, sub.ID, "https://yadi.sk/d/test", "synced"); err != nil {
		t.Fatal(err)
	}
	synced, err := st.Subscription(ctx, sub.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if !synced.UpdatedAt.Equal(touched.UpdatedAt) {
		t.Fatalf("mirror status changed content revision: before=%s after=%s", touched.UpdatedAt, synced.UpdatedAt)
	}
	if synced.MirrorPublicURL != "https://yadi.sk/d/test" || synced.MirrorStatus != "synced" {
		t.Fatalf("mirror state was not stored: %#v", synced)
	}
}
