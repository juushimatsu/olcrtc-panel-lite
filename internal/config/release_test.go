package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestApplyInstalledRelease(t *testing.T) {
	releaseDir := t.TempDir()
	current := filepath.Join(releaseDir, "current")
	if err := os.MkdirAll(current, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := []byte(`{"bundle_id":"bundle-test","panel_version":"0.2.0","upstream_sha":"0123456789abcdef0123456789abcdef01234567","build_time":"2026-07-17T00:00:00Z"}`)
	if err := os.WriteFile(filepath.Join(current, "manifest.json"), manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := Default()
	cfg.ReleaseDir = releaseDir
	cfg.PanelVersion = "stale"
	cfg.UpstreamSHA = "stale"
	ApplyInstalledRelease(&cfg)
	if cfg.PanelVersion != "0.2.0" || cfg.UpstreamSHA != "0123456789abcdef0123456789abcdef01234567" {
		t.Fatalf("installed release was not applied: %#v", cfg)
	}
}
