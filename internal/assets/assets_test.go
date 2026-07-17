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

func TestInstanceUnitSelfHealsPermissionsAndOperationsExposeState(t *testing.T) {
	unit, err := fs.ReadFile(files, "files/systemd/olcrtc-instance@.service")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(unit), "ExecStartPre=+/usr/local/bin/olcrtc-panel instance prepare") {
		t.Fatal("instance unit does not prepare permissions before start")
	}
	updater, err := fs.ReadFile(files, "files/update/update.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(updater), "STATE_FILE=/run/olcrtc-panel-update-state.json") || !strings.Contains(string(updater), "write_state completed") {
		t.Fatal("update script does not publish progress")
	}
	wbInstaller, err := fs.ReadFile(files, "files/wb/install-components.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(wbInstaller), "write_state packages") {
		t.Fatal("WB installer does not publish package progress")
	}
}

func TestLiveKitAndWBUnitsHaveRequiredRuntimeAccess(t *testing.T) {
	instanceUnit, err := fs.ReadFile(files, "files/systemd/olcrtc-instance@.service")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(instanceUnit), "RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX AF_NETLINK") {
		t.Fatal("instance unit blocks AF_NETLINK required for LiveKit ICE interface discovery")
	}

	wbUnit, err := fs.ReadFile(files, "files/systemd/olcrtc-wb-session.service")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(wbUnit), "ExecStart=/bin/bash /usr/lib/olcrtc-panel/wb/run-session.sh") {
		t.Fatal("WB session does not invoke its runner through bash")
	}

	wbInstaller, err := fs.ReadFile(files, "files/wb/install-components.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(wbInstaller), "chmod -R a+rX,go-w \"$INSTALL_DIR\"") {
		t.Fatal("WB runtime tree is not made readable and traversable for olcrtc-wb")
	}
}
