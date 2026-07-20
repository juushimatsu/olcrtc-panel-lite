package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/juushimatsu/olcrtc-panel-lite/internal/model"
)

func TestClientIDInvariantAndLegacyColumnRemoval(t *testing.T) {
	st := openTestStore(t)
	item, err := st.CreateInstance(context.Background(), model.Instance{Name: "client-id", Provider: "jitsi", Transport: "datachannel", RoomID: "https://meet.example/r", DNS: "8.8.8.8:53", ResetPolicy: "never"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := uuid.Parse(item.ClientID); err != nil {
		t.Fatalf("client_id is not a UUID: %q: %v", item.ClientID, err)
	}
	rows, err := st.db.Query(`PRAGMA table_info(subscription_entries)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, kind string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatal(err)
		}
		if name == "exclave_compatible" {
			t.Fatal("legacy compatibility column still exists after migration")
		}
	}
}

func TestMigrationAssignsUUIDToExistingInstance(t *testing.T) {
	path := filepath.Join(t.TempDir(), "panel.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := migrations.ReadFile("migrations/001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(initial)); err != nil {
		t.Fatal(err)
	}
	now := formatTime(time.Now())
	if _, err := db.Exec(`INSERT INTO schema_migrations(version, applied_at) VALUES(1, ?); INSERT INTO instances(name, provider, transport, room_id, dns, transport_options_json, liveness_json, traffic_options_json, created_at, updated_at) VALUES('existing', 'jitsi', 'datachannel', 'https://meet.example/r', '8.8.8.8:53', '{}', '{}', '{}', ?, ?)`, now, now, now); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	item, err := st.Instance(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := uuid.Parse(item.ClientID)
	if err != nil || parsed.Version() != 4 || parsed.Variant() != uuid.RFC4122 {
		t.Fatalf("migrated client_id = %q (%v)", item.ClientID, err)
	}
}
