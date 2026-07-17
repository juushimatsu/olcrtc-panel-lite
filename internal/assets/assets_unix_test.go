//go:build !windows

package assets

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestInstallRepairsDirectoryModesUnderRestrictiveUmask(t *testing.T) {
	root := t.TempDir()
	oldUmask := syscall.Umask(0o077)
	defer syscall.Umask(oldUmask)

	if err := Install(root); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"etc",
		"etc/systemd",
		"etc/systemd/system",
		"usr",
		"usr/lib",
		"usr/lib/olcrtc-panel",
		"usr/lib/olcrtc-panel/wb",
	} {
		info, err := os.Stat(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			t.Fatalf("stat %s: %v", name, err)
		}
		if got := info.Mode().Perm(); got != 0o755 {
			t.Fatalf("directory %s mode = %04o, want 0755", name, got)
		}
	}

	runner, err := os.Stat(filepath.Join(root, "usr", "lib", "olcrtc-panel", "wb", "run-session.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if got := runner.Mode().Perm(); got != 0o755 {
		t.Fatalf("run-session.sh mode = %04o, want 0755", got)
	}
}

func TestInstallMigratesExistingWBRuntime(t *testing.T) {
	root := t.TempDir()
	runtimeDir := filepath.Join(root, "opt", "olcrtc-panel", "wb")
	playwrightDir := filepath.Join(runtimeDir, "node_modules", "playwright")
	if err := os.MkdirAll(playwrightDir, 0o700); err != nil {
		t.Fatal(err)
	}
	moduleFile := filepath.Join(playwrightDir, "index.js")
	if err := os.WriteFile(moduleFile, []byte("module.exports = {};\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := Install(root); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{runtimeDir, filepath.Dir(playwrightDir), playwrightDir} {
		info, err := os.Stat(name)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o755 {
			t.Fatalf("directory %s mode = %04o, want 0755", name, got)
		}
	}
	moduleInfo, err := os.Stat(moduleFile)
	if err != nil {
		t.Fatal(err)
	}
	if got := moduleInfo.Mode().Perm(); got != 0o644 {
		t.Fatalf("module mode = %04o, want 0644", got)
	}
	workerInfo, err := os.Stat(filepath.Join(runtimeDir, "worker.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	if got := workerInfo.Mode().Perm(); got != 0o644 {
		t.Fatalf("worker mode = %04o, want 0644", got)
	}
}
