package systemd

import (
	"context"
	"testing"
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
