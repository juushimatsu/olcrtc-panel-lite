// Package systemd executes a fixed allowlist of systemctl and journalctl commands.
package systemd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Status describes the source-of-truth service state.
type Status struct {
	State         string `json:"state"`
	SubState      string `json:"sub_state"`
	UptimeSeconds int64  `json:"uptime_seconds"`
	IngressBytes  int64  `json:"ingress_bytes"`
	EgressBytes   int64  `json:"egress_bytes"`
}

// Controller is the restricted lifecycle interface used by the panel.
type Controller interface {
	Status(context.Context, int64) (Status, error)
	Start(context.Context, int64) error
	Stop(context.Context, int64) error
	Restart(context.Context, int64) error
	Disable(context.Context, int64) error
	Logs(context.Context, int64, int) (string, error)
}

// Manager calls systemd directly without shell interpolation.
type Manager struct {
	enabled bool
	mu      sync.Mutex
	states  map[int64]Status
}

// New returns a systemd controller. Disabled mode is safe for development.
func New(enabled bool) *Manager { return &Manager{enabled: enabled, states: make(map[int64]Status)} }

func unit(id int64) (string, error) {
	if id < 1 {
		return "", errors.New("instance ID must be positive")
	}
	value := strconv.FormatInt(id, 10)
	for _, r := range value {
		if r < '0' || r > '9' {
			return "", errors.New("invalid instance ID")
		}
	}
	return "olcrtc-instance@" + value + ".service", nil
}

// Status reads ActiveState from systemd.
func (m *Manager) Status(ctx context.Context, id int64) (Status, error) {
	if !m.enabled || runtime.GOOS != "linux" {
		m.mu.Lock()
		defer m.mu.Unlock()
		status, ok := m.states[id]
		if !ok {
			status = Status{State: "stopped", SubState: "development"}
		}
		return status, nil
	}
	name, err := unit(id)
	if err != nil {
		return Status{}, err
	}
	out, err := exec.CommandContext(ctx, "systemctl", "show", name, "--property=ActiveState,SubState,ExecMainStartTimestampMonotonic,ActiveEnterTimestampMonotonic,ActiveEnterTimestamp,IPIngressBytes,IPEgressBytes").Output()
	if err != nil {
		return Status{State: "unknown"}, fmt.Errorf("systemctl show: %w", err)
	}
	values := ParseShow(string(out))
	status := Status{State: "unknown"}
	status.State = mapState(values["ActiveState"])
	status.SubState = values["SubState"]
	if status.State == "running" {
		started := values["ExecMainStartTimestampMonotonic"]
		if started == "" || started == "0" {
			started = values["ActiveEnterTimestampMonotonic"]
		}
		if nowMicros, ok := monotonicMicros(); ok {
			status.UptimeSeconds = elapsedMonotonic(started, nowMicros)
		}
		if status.UptimeSeconds == 0 {
			status.UptimeSeconds = uptimeFromWallClock(values["ActiveEnterTimestamp"])
		}
	}
	status.IngressBytes, _ = strconv.ParseInt(values["IPIngressBytes"], 10, 64)
	status.EgressBytes, _ = strconv.ParseInt(values["IPEgressBytes"], 10, 64)
	return status, nil
}

func elapsedMonotonic(started string, nowMicros uint64) int64 {
	startMicros, err := strconv.ParseUint(strings.TrimSpace(started), 10, 64)
	if err != nil || startMicros == 0 || nowMicros <= startMicros {
		return 0
	}
	return int64((nowMicros - startMicros) / 1_000_000)
}

// uptimeFromWallClock parses the systemd ActiveEnterTimestamp wall-clock value
// and returns elapsed seconds.  The format systemd emits is
// "Mon 2006-01-02 15:04:05 MST" (en_US locale) or an empty/n/a string for
// units that have never been active.
func uptimeFromWallClock(entered string) int64 {
	entered = strings.TrimSpace(entered)
	if entered == "" || entered == "n/a" {
		return 0
	}
	// systemd prints the day-of-week abbreviated name before the date.
	// Try both with and without day prefix to be locale-tolerant.
	formats := []string{
		"Mon 2006-01-02 15:04:05 MST",
		"2006-01-02 15:04:05 MST",
	}
	for _, layout := range formats {
		t, err := time.Parse(layout, entered)
		if err == nil {
			if s := int64(time.Since(t).Seconds()); s > 0 {
				return s
			}
			return 0
		}
	}
	return 0
}

func mapState(active string) string {
	switch active {
	case "active":
		return "running"
	case "inactive":
		return "stopped"
	case "activating":
		return "starting"
	case "deactivating":
		return "stopping"
	case "failed":
		return "failed"
	default:
		return "unknown"
	}
}

// Start starts one fixed instance unit.
func (m *Manager) Start(ctx context.Context, id int64) error {
	return m.action(ctx, "start", id)
}

// Stop stops one fixed instance unit.
func (m *Manager) Stop(ctx context.Context, id int64) error {
	return m.action(ctx, "stop", id)
}

// Restart restarts one fixed instance unit.
func (m *Manager) Restart(ctx context.Context, id int64) error {
	return m.action(ctx, "restart", id)
}

// Disable disables one fixed instance unit.
func (m *Manager) Disable(ctx context.Context, id int64) error {
	return m.action(ctx, "disable", id)
}

func (m *Manager) action(ctx context.Context, action string, id int64) error {
	if !m.enabled || runtime.GOOS != "linux" {
		m.mu.Lock()
		switch action {
		case "start", "restart":
			m.states[id] = Status{State: "running", SubState: "development"}
		case "stop", "disable":
			m.states[id] = Status{State: "stopped", SubState: "development"}
		}
		m.mu.Unlock()
		return nil
	}
	switch action {
	case "start", "stop", "restart", "disable":
	default:
		return errors.New("systemd action is not allowed")
	}
	name, err := unit(id)
	if err != nil {
		return err
	}
	if action == "start" || action == "restart" {
		_ = exec.CommandContext(ctx, "systemctl", "reset-failed", name).Run()
	}
	out, err := exec.CommandContext(ctx, "systemctl", action, name).CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl %s: %s: %w", action, strings.TrimSpace(string(out)), err)
	}
	return nil
}

// Logs returns a bounded plain-text journal excerpt.
func (m *Manager) Logs(ctx context.Context, id int64, lines int) (string, error) {
	if lines < 1 || lines > 2000 {
		lines = 200
	}
	if !m.enabled || runtime.GOOS != "linux" {
		return "Журнал systemd доступен только на Linux при включённом systemd.\n", nil
	}
	name, err := unit(id)
	if err != nil {
		return "", err
	}
	command := exec.CommandContext(ctx, "journalctl", "--no-pager", "--output=short-iso", "--lines", strconv.Itoa(lines), "--unit", name)
	out, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("journalctl: %w", err)
	}
	return string(out), nil
}

// WaitActive waits for a running state without starting unbounded goroutines.
func WaitActive(ctx context.Context, controller Controller, id int64, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()
	runningSince := time.Time{}
	for {
		status, err := controller.Status(ctx, id)
		if err == nil && status.State == "running" {
			if runningSince.IsZero() {
				runningSince = time.Now()
			}
			if time.Since(runningSince) >= time.Second {
				return nil
			}
		} else {
			runningSince = time.Time{}
		}
		if status.State == "failed" {
			return errors.New("instance entered failed state")
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for active state: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

// ParseShow is used by tests and diagnostics for key-value systemd output.
func ParseShow(input string) map[string]string {
	values := make(map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(input))
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), "=")
		if ok {
			values[key] = value
		}
	}
	return values
}
