package assets

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstall(t *testing.T) {
	root := t.TempDir()
	if err := Install(root); err != nil {
		t.Fatal(err)
	}
	paths := []string{"etc/systemd/system/olcrtc-panel.service", "etc/systemd/system/olcrtc-instance@.service", "usr/lib/olcrtc-panel/wb/worker.mjs", "usr/lib/olcrtc-panel/update.sh"}
	for _, name := range paths {
		info, err := os.Stat(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil || info.IsDir() {
			t.Fatalf("asset %s: %v", name, err)
		}
	}
}

func TestPathWithinRoot(t *testing.T) {
	volumeRoot := filepath.VolumeName(t.TempDir()) + string(filepath.Separator)
	if !pathWithinRoot(volumeRoot, filepath.Join(volumeRoot, "etc", "systemd", "system")) {
		t.Fatal("filesystem-root child was rejected")
	}

	root := filepath.Join(t.TempDir(), "root")
	for _, test := range []struct {
		name   string
		target string
		inside bool
	}{
		{name: "root", target: root, inside: true},
		{name: "child", target: filepath.Join(root, "usr", "lib"), inside: true},
		{name: "parent", target: filepath.Dir(root), inside: false},
		{name: "sibling prefix", target: root + "-other", inside: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := pathWithinRoot(root, test.target); got != test.inside {
				t.Fatalf("pathWithinRoot(%q, %q) = %v, want %v", root, test.target, got, test.inside)
			}
		})
	}
}
