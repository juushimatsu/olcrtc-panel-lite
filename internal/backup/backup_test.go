package backup

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/openlibrecommunity/olcrtc-panel-lite/internal/store"
)

func TestCreateOrdinaryBackup(t *testing.T) {
	root := t.TempDir()
	st, err := store.Open(filepath.Join(root, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	instances := filepath.Join(root, "instances", "1")
	if err := os.MkdirAll(instances, 0o750); err != nil {
		t.Fatal(err)
	}
	config := []byte("mode: srv\nauth:\n  provider: wbstream\n  token: secret-token\nsocks:\n  proxy_pass: secret-pass\n")
	if err := os.WriteFile(filepath.Join(instances, "config.yaml"), config, 0o640); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(st.DB(), filepath.Join(root, "instances"), filepath.Join(root, "backups"))
	archive, err := manager.Create(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(archive); err != nil || info.Size() == 0 {
		t.Fatalf("archive info=%v err=%v", info, err)
	}
}
