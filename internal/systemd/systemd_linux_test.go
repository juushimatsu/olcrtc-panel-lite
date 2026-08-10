//go:build linux

package systemd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStartIgnoresResetFailedForUnloadedTemplateInstance(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "systemctl.log")
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$SYSTEMCTL_TEST_LOG"
if [ "$1" = "reset-failed" ]; then
    exit 1
fi
exit 0
`
	if err := os.WriteFile(filepath.Join(dir, "systemctl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SYSTEMCTL_TEST_LOG", logPath)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	if err := New(true).Start(context.Background(), 2); err != nil {
		t.Fatalf("start failed after best-effort reset-failed: %v", err)
	}
	b, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"reset-failed olcrtc-instance@2.service",
		"start olcrtc-instance@2.service",
	}
	if got := strings.FieldsFunc(strings.TrimSpace(string(b)), func(r rune) bool { return r == '\n' || r == '\r' }); !equalStrings(got, want) {
		t.Fatalf("systemctl calls = %q, want %q", got, want)
	}
}

func TestStatusUsesMonotonicProcessStartTimestamp(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "systemctl.log")
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$SYSTEMCTL_TEST_LOG"
cat <<'EOF'
ActiveState=active
SubState=running
ExecMainStartTimestampMonotonic=1
ActiveEnterTimestampMonotonic=1
ActiveEnterTimestamp=n/a
IPIngressBytes=12
IPEgressBytes=34
EOF
`
	if err := os.WriteFile(filepath.Join(dir, "systemctl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SYSTEMCTL_TEST_LOG", logPath)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	status, err := New(true).Status(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != "running" || status.UptimeSeconds <= 0 || status.IngressBytes != 12 || status.EgressBytes != 34 {
		t.Fatalf("unexpected status: %#v", status)
	}
	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logged), "ExecMainStartTimestampMonotonic") {
		t.Fatalf("systemctl properties do not request process monotonic start: %s", logged)
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
