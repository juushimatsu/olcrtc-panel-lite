package systemd

import (
	"context"
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
