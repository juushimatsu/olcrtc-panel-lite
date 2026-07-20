package main

import (
	"os"
	"path/filepath"
	"testing"
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
