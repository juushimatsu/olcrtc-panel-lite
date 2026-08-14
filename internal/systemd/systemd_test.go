package systemd

import (
	"context"
	"strconv"
	"testing"
	"time"
)

func TestUnitValidation(t *testing.T) {
	if got, err := unit(12); err != nil || got != "olcrtc-instance@12.service" {
		t.Fatalf("unit=%q err=%v", got, err)
	}
	if _, err := unit(0); err == nil {
		t.Fatal("zero ID accepted")
	}
}
func TestDisabledManagerLifecycle(t *testing.T) {
	m := New(false)
	ctx := context.Background()
	if err := m.Start(ctx, 1); err != nil {
		t.Fatal(err)
	}
	status, _ := m.Status(ctx, 1)
	if status.State != "running" {
		t.Fatalf("state=%s", status.State)
	}
	if err := m.Stop(ctx, 1); err != nil {
		t.Fatal(err)
	}
	status, _ = m.Status(ctx, 1)
	if status.State != "stopped" {
		t.Fatalf("state=%s", status.State)
	}
}

type flappingController struct {
	Controller
	calls int
}

func (c *flappingController) Status(context.Context, int64) (Status, error) {
	c.calls++
	if c.calls == 1 {
		return Status{State: "running"}, nil
	}
	return Status{State: "failed"}, nil
}

func TestWaitActiveRejectsTransientRunningState(t *testing.T) {
	controller := &flappingController{}
	if err := WaitActive(context.Background(), controller, 1, time.Second); err == nil {
		t.Fatal("transient running state was accepted")
	}
}

func TestElapsedMonotonicUsesProcessStartInsteadOfPanelLifetime(t *testing.T) {
	tests := []struct {
		name       string
		started    string
		nowMicros  uint64
		wantSecond int64
	}{
		{name: "running", started: "5000000", nowMicros: 12_750_000, wantSecond: 7},
		{name: "subsecond", started: "12000000", nowMicros: 12_750_000, wantSecond: 0},
		{name: "future", started: "13000000", nowMicros: 12_750_000, wantSecond: 0},
		{name: "invalid", started: "n/a", nowMicros: 12_750_000, wantSecond: 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := elapsedMonotonic(test.started, test.nowMicros); got != test.wantSecond {
				t.Fatalf("elapsedMonotonic() = %d, want %d", got, test.wantSecond)
			}
		})
	}
}

func TestStatusFromShowSeparatesServiceAndProcessUptime(t *testing.T) {
	observedAt := time.Date(2026, 8, 14, 12, 0, 0, 500_000_000, time.UTC)
	status := statusFromShow(map[string]string{
		"ActiveState":                     "active",
		"SubState":                        "running",
		"ActiveEnterTimestampMonotonic":   "5000000",
		"ExecMainStartTimestampMonotonic": "8250000",
		"MainPID":                         "4321",
		"NRestarts":                       "2",
		"InvocationID":                    "invocation-a",
		"IPIngressBytes":                  "12",
		"IPEgressBytes":                   "34",
	}, 20_750_000, true, observedAt)

	if status.UptimeSeconds != 15 || status.ProcessUptimeSeconds != 12 {
		t.Fatalf("uptime=%d process=%d", status.UptimeSeconds, status.ProcessUptimeSeconds)
	}
	if status.UptimeSource != "active_enter_monotonic" || status.ProcessUptimeSource != "exec_main_start_monotonic" {
		t.Fatalf("unexpected sources: %#v", status)
	}
	if status.StartedAt == nil || !status.StartedAt.Equal(observedAt.Add(-15_750*time.Millisecond)) {
		t.Fatalf("started_at=%v", status.StartedAt)
	}
	if status.MainPID != 4321 || status.RestartCount != 2 || status.InvocationID != "invocation-a" {
		t.Fatalf("runtime identity=%#v", status)
	}
}

func TestStatusFromShowKeepsServiceStartAcrossPolls(t *testing.T) {
	values := map[string]string{
		"ActiveState":                   "active",
		"ActiveEnterTimestampMonotonic": "40000000",
		"MainPID":                       "100",
	}
	firstObserved := time.Unix(1_700_000_000, 0).UTC()
	first := statusFromShow(values, 100_000_000, true, firstObserved)
	second := statusFromShow(values, 110_000_000, true, firstObserved.Add(10*time.Second))
	if first.StartedAt == nil || second.StartedAt == nil || !first.StartedAt.Equal(*second.StartedAt) {
		t.Fatalf("service start changed across polls: first=%v second=%v", first.StartedAt, second.StartedAt)
	}
}

func TestStatusFromShowRestartResetsOnlyProcessUptime(t *testing.T) {
	observedAt := time.Unix(1_700_000_000, 0).UTC()
	before := statusFromShow(map[string]string{
		"ActiveState":                     "active",
		"ActiveEnterTimestampMonotonic":   "40000000",
		"ExecMainStartTimestampMonotonic": "50000000",
		"MainPID":                         "100",
		"NRestarts":                       "0",
		"InvocationID":                    "before",
	}, 100_000_000, true, observedAt)
	after := statusFromShow(map[string]string{
		"ActiveState":                     "active",
		"ActiveEnterTimestampMonotonic":   "40000000",
		"ExecMainStartTimestampMonotonic": "99000000",
		"MainPID":                         "200",
		"NRestarts":                       "1",
		"InvocationID":                    "after",
	}, 100_000_000, true, observedAt)
	if before.UptimeSeconds != after.UptimeSeconds || before.ProcessUptimeSeconds != 50 || after.ProcessUptimeSeconds != 1 {
		t.Fatalf("unexpected restart uptimes: before=%#v after=%#v", before, after)
	}
	if before.InvocationID == after.InvocationID || before.MainPID == after.MainPID || before.RestartCount == after.RestartCount {
		t.Fatalf("restart identity did not change: before=%#v after=%#v", before, after)
	}
}

func TestStatusFromShowInactiveReturnsZero(t *testing.T) {
	status := statusFromShow(map[string]string{
		"ActiveState":                     "failed",
		"ActiveEnterTimestampMonotonic":   "1",
		"ExecMainStartTimestampMonotonic": "1",
		"MainPID":                         "123",
	}, 10_000_000, true, time.Unix(100, 0))
	if status.State != "failed" || status.UptimeSeconds != 0 || status.ProcessUptimeSeconds != 0 || status.UptimeSource != "inactive" {
		t.Fatalf("inactive status=%#v", status)
	}
}

func TestStatusFromShowUsesNumericWallClockFallback(t *testing.T) {
	observedAt := time.Date(2026, 8, 14, 12, 5, 0, 0, time.UTC)
	startedAt := observedAt.Add(-90 * time.Second)
	status := statusFromShow(map[string]string{
		"ActiveState":              "active",
		"ActiveEnterTimestampUSec": strconv.FormatInt(startedAt.UnixMicro(), 10),
		"MainPID":                  "123",
	}, 0, false, observedAt)
	if status.UptimeSeconds != 90 || status.UptimeSource != "active_enter_usec" || status.StartedAt == nil || !status.StartedAt.Equal(startedAt) {
		t.Fatalf("fallback status=%#v", status)
	}
	if status.ProcessUptimeSeconds != 0 || status.ProcessUptimeSource != "unavailable" {
		t.Fatalf("fallback invented process uptime: %#v", status)
	}
}

func TestSystemdTimestampUsesLocaleIndependentUnixValues(t *testing.T) {
	want := time.Unix(1_723_636_800, 123_456_000).UTC()
	for _, value := range []string{"@1723636800.123456", "1723636800123456"} {
		got, ok := systemdTimestampUSec(value)
		if !ok || !got.Equal(want) {
			t.Fatalf("systemdTimestampUSec(%q)=(%v,%v), want %v", value, got, ok, want)
		}
	}
}
