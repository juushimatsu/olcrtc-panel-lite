package assets

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
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

func TestWBWorkerLoadsPinnedPlaywrightAndReadsAuthorization(t *testing.T) {
	b, err := fs.ReadFile(files, "files/wb/worker.mjs")
	if err != nil {
		t.Fatal(err)
	}
	source := string(b)
	for _, required := range []string{
		"require('/opt/olcrtc-panel/wb/node_modules/playwright')",
		"request.headerValue('authorization')",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("worker is missing %q", required)
		}
	}
	if strings.Contains(source, "from 'playwright'") {
		t.Fatal("worker uses bare Playwright import outside its node_modules tree")
	}
}

func TestInstanceRuntimeAssetsPreserveExecutableAccessAndBoundRestarts(t *testing.T) {
	unit, err := fs.ReadFile(files, "files/systemd/olcrtc-instance@.service")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"StartLimitBurst=3", "RestartPreventExitStatus=203"} {
		if !strings.Contains(string(unit), required) {
			t.Fatalf("instance unit is missing %q", required)
		}
	}

	updater, err := fs.ReadFile(files, "files/update/update.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"chmod 0710 \"$directory\"",
		"chown root:olcrtc \"$directory/olcrtc\"",
		"chmod 0710 /etc/olcrtc-panel",
		"chmod 0640 \"$file\"",
	} {
		if !strings.Contains(string(updater), required) {
			t.Fatalf("updater is missing %q", required)
		}
	}
}
