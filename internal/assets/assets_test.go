package assets

import (
	"errors"
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
		"request.headers()['authorization']",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("worker is missing %q", required)
		}
	}
	if strings.Contains(source, "from 'playwright'") {
		t.Fatal("worker uses bare Playwright import outside its node_modules tree")
	}
	if strings.Contains(source, "request.headerValue('authorization')") {
		t.Fatal("worker uses asynchronous header lookup inside an unawaited Playwright event callback")
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

func TestUpdaterUsesEffectivePanelHealthURLAcrossSwitches(t *testing.T) {
	updater, err := fs.ReadFile(files, "files/update/update.sh")
	if err != nil {
		t.Fatal(err)
	}
	source := string(updater)
	for _, required := range []string{
		`"$binary" health-url --config "$CONFIG"`,
		`*'unknown command "health-url"'*)`,
		`health_url=$(panel_health_url "$target/olcrtc-panel")`,
		`rollback_health_url=$(panel_health_url "$current/olcrtc-panel")`,
		`health_url=$(panel_health_url "$previous/olcrtc-panel")`,
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("updater is missing %q", required)
		}
	}
	healthURL := strings.Index(source, `health_url=$(panel_health_url "$target/olcrtc-panel")`)
	switchBundle := strings.Index(source, `ln -sfn "$target" "$RELEASES/current"`)
	if healthURL < 0 || switchBundle < 0 || healthURL > switchBundle {
		t.Fatal("updater does not resolve the target health URL before switching bundles")
	}
	if count := strings.Count(source, `/usr/local/bin/olcrtc-panel assets install --root /`); count != 3 {
		t.Fatalf("updater installs active bundle assets %d times, want install and both rollback paths", count)
	}
	if count := strings.Count(source, `systemctl daemon-reload`); count != 3 {
		t.Fatalf("updater reloads systemd %d times, want install and both rollback paths", count)
	}
	if !strings.Contains(source, `wait_for_panel "$rollback_health_url"`) || strings.Count(source, `wait_for_panel "$health_url"`) != 2 {
		t.Fatal("updater does not use the matching effective health URL for install and both rollback paths")
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

func TestWBWorkerRunsBesideInstalledPlaywright(t *testing.T) {
	installer, err := fs.ReadFile(files, "files/wb/install-components.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(installer), `"$INSTALL_DIR/worker.mjs"`) {
		t.Fatal("WB installer does not copy the worker beside node_modules")
	}
	runner, err := fs.ReadFile(files, "files/wb/run-session.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(runner), "/opt/olcrtc-panel/wb/worker.mjs") {
		t.Fatal("WB runner does not execute the runtime worker beside Playwright")
	}
}

func TestRefreshWBAutomationUpdatesInstalledWorkerAndRuntimeFiles(t *testing.T) {
	root := t.TempDir()
	runtimeDir := filepath.Join(root, "opt", "olcrtc-panel", "wb")
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtimeDir, "worker.mjs"), []byte("stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := RefreshWBAutomation(root); err != nil {
		t.Fatal(err)
	}
	embeddedWorker, err := fs.ReadFile(files, "files/wb/worker.mjs")
	if err != nil {
		t.Fatal(err)
	}
	installedWorker, err := os.ReadFile(filepath.Join(runtimeDir, "worker.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	if string(installedWorker) != string(embeddedWorker) {
		t.Fatal("installed WB worker was not refreshed from the current binary")
	}
	for _, name := range []string{
		filepath.Join("usr", "lib", "olcrtc-panel", "wb", "worker.mjs"),
		filepath.Join("usr", "lib", "olcrtc-panel", "wb", "run-session.sh"),
		filepath.Join("etc", "systemd", "system", "olcrtc-wb-session.service"),
	} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatalf("refreshed WB asset %s: %v", name, err)
		}
	}
}

func TestLegacyWBProfileMigrationIsOneTimeAndNonDestructive(t *testing.T) {
	root := t.TempDir()
	stateRoot := filepath.Join(root, "var", "lib", "olcrtc-wb")
	legacy := filepath.Join(stateRoot, "profile")
	target := filepath.Join(stateRoot, "profiles", "wbstream")
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "legacy-cookie"), []byte("legacy"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := migrateLegacyWBProfile(root); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(target, "legacy-cookie")); err != nil || string(data) != "legacy" {
		t.Fatalf("legacy profile was not migrated: data=%q err=%v", data, err)
	}

	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "second-cookie"), []byte("second"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := migrateLegacyWBProfile(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(target, "second-cookie")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("existing WB profile was overwritten: %v", err)
	}
	if _, err := os.Stat(filepath.Join(legacy, "second-cookie")); err != nil {
		t.Fatalf("second legacy profile should remain untouched: %v", err)
	}
}

func TestRefreshWBAutomationPreservesProviderProfiles(t *testing.T) {
	root := t.TempDir()
	profiles := filepath.Join(root, "var", "lib", "olcrtc-wb", "profiles")
	markers := []string{
		filepath.Join(profiles, "wbstream", "cookie.sqlite"),
		filepath.Join(profiles, "telemost", "cookie.sqlite"),
	}
	for _, marker := range markers {
		if err := os.MkdirAll(filepath.Dir(marker), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(marker, []byte(marker), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := RefreshWBAutomation(root); err != nil {
		t.Fatal(err)
	}
	for _, marker := range markers {
		if data, err := os.ReadFile(marker); err != nil || string(data) != marker {
			t.Fatalf("profile marker changed: %s data=%q err=%v", marker, data, err)
		}
	}
}

func TestWBSessionUsesEphemeralRuntimeState(t *testing.T) {
	worker, err := fs.ReadFile(files, "files/wb/worker.mjs")
	if err != nil {
		t.Fatal(err)
	}
	runner, err := fs.ReadFile(files, "files/wb/run-session.sh")
	if err != nil {
		t.Fatal(err)
	}
	for name, source := range map[string]string{"worker": string(worker), "runner": string(runner)} {
		if !strings.Contains(source, "/run/olcrtc-wb/job.json") {
			t.Fatalf("%s does not use the reference runtime job path", name)
		}
		if strings.Contains(source, "/var/lib/olcrtc-wb/job.json") {
			t.Fatalf("%s still uses persistent storage for the session job", name)
		}
	}
	panelUnit, err := fs.ReadFile(files, "files/systemd/olcrtc-panel.service")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"RuntimeDirectory=olcrtc-wb", "/run/olcrtc-wb"} {
		if !strings.Contains(string(panelUnit), path) {
			t.Fatalf("panel service does not allow WB path %s", path)
		}
	}
}

func TestWBSessionRepairsSharedRuntimeOwnershipBeforeRunner(t *testing.T) {
	unit, err := fs.ReadFile(files, "files/systemd/olcrtc-wb-session.service")
	if err != nil {
		t.Fatal(err)
	}
	required := "ExecStartPre=+/usr/bin/chown -R --no-dereference olcrtc-wb:olcrtc-wb /run/olcrtc-wb"
	if !strings.Contains(string(unit), required) {
		t.Fatalf("WB session unit is missing ownership repair %q", required)
	}
}

func TestWBWorkerCarriesSessionActionIntoResultState(t *testing.T) {
	worker, err := fs.ReadFile(files, "files/wb/worker.mjs")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(worker), "action: job.action") {
		t.Fatal("WB worker state does not identify create versus refresh sessions")
	}
}

func TestWorkerSupportsTelemostCreationWithManualYandexLogin(t *testing.T) {
	worker, err := fs.ReadFile(files, "files/wb/worker.mjs")
	if err != nil {
		t.Fatal(err)
	}
	source := string(worker)
	for _, required := range []string{
		"provider === 'telemost'",
		`[data-testid="create-call-button"]`,
		`[data-testid="add-user-login-input"]`,
		"https://telemost.yandex.ru",
		"findTelemostRoomID",
		"Комната Telemost создана",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("Telemost worker flow is missing %q", required)
		}
	}
}
