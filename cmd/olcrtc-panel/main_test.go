package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/juushimatsu/olcrtc-panel-lite/internal/config"
	"github.com/juushimatsu/olcrtc-panel-lite/internal/store"
	"gopkg.in/yaml.v3"
)

func TestAssetsRefreshWBAction(t *testing.T) {
	root := t.TempDir()
	runtimeDir := filepath.Join(root, "opt", "olcrtc-panel", "wb")
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	workerPath := filepath.Join(runtimeDir, "worker.mjs")
	if err := os.WriteFile(workerPath, []byte("stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := installAssets([]string{"refresh-wb", "--root", root}); err != nil {
		t.Fatal(err)
	}
	worker, err := os.ReadFile(workerPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(worker) == "stale\n" {
		t.Fatal("refresh-wb left the stale installed worker in place")
	}
	if _, err := os.Stat(filepath.Join(root, "etc", "systemd", "system", "olcrtc-wb-session.service")); err != nil {
		t.Fatalf("refresh-wb did not install the WB session unit: %v", err)
	}
}

func TestAssetsRejectsUnknownAction(t *testing.T) {
	if err := installAssets([]string{"unknown", "--root", t.TempDir()}); err == nil {
		t.Fatal("unknown assets action was accepted")
	}
}

func TestApplyStoredPublicPortKeepsExplicitListenPort(t *testing.T) {
	tests := []struct {
		name       string
		cfg        config.Config
		storedPort int
		wantListen string
	}{
		{name: "legacy coupled", cfg: config.Config{Listen: "0.0.0.0:8443", PublicPort: 8443}, storedPort: 9443, wantListen: "0.0.0.0:9443"},
		{name: "reverse proxy", cfg: config.Config{Listen: "127.0.0.1:8443", PublicPort: 443}, storedPort: 444, wantListen: "127.0.0.1:8443"},
		{name: "ipv6 reverse proxy", cfg: config.Config{Listen: "[::1]:8443", PublicPort: 443}, storedPort: 444, wantListen: "[::1]:8443"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			applyStoredPublicPort(&test.cfg, test.storedPort)
			if test.cfg.PublicPort != test.storedPort || test.cfg.Listen != test.wantListen {
				t.Fatalf("config=%#v want listen=%q public_port=%d", test.cfg, test.wantListen, test.storedPort)
			}
		})
	}
}

func TestHealthURLUsesEffectiveStoredSettings(t *testing.T) {
	root := t.TempDir()
	cfg := config.Dev(root)
	cfg.Listen = "0.0.0.0:8443"
	cfg.PublicPort = 8443
	configPath := filepath.Join(root, "config.yaml")
	body, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(cfg.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSettings(context.Background(), map[string]string{
		"panel_path":        "/control-a8f3",
		"subscription_path": "/feeds-b19c",
		"public_port":       "9443",
	}); err != nil {
		st.Close()
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	got, err := healthURL([]string{"--config", configPath})
	if err != nil {
		t.Fatal(err)
	}
	if want := "https://127.0.0.1:9443/control-a8f3/"; got != want {
		t.Fatalf("health URL = %q, want %q", got, want)
	}
}

func TestHealthURLUsesIPv6LoopbackForWildcardListen(t *testing.T) {
	root := t.TempDir()
	cfg := config.Dev(root)
	cfg.Listen = "[::]:8443"
	cfg.PanelPath = "/control"
	configPath := filepath.Join(root, "config.yaml")
	body, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(cfg.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	got, err := healthURL([]string{"--config", configPath})
	if err != nil {
		t.Fatal(err)
	}
	if want := "https://[::1]:8443/control/"; got != want {
		t.Fatalf("health URL = %q, want %q", got, want)
	}
}
