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
