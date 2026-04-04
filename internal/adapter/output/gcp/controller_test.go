package gcp_test

import (
	"context"
	"testing"

	"github.com/hironow/runops-gateway/internal/adapter/output/gcp"
	"github.com/hironow/runops-gateway/internal/core/port"
)

// Compile-time check: Controller must satisfy port.GCPController.
var _ port.GCPController = (*gcp.Controller)(nil)

// newTestController creates a Controller for testing.
// In CI without GCP credentials, NewController may fail — tests below handle both cases.
func newTestController(t *testing.T) *gcp.Controller {
	t.Helper()
	ctrl, err := gcp.NewController(context.Background())
	if err != nil {
		t.Skipf("skipping: cannot create GCP controller (no credentials?): %v", err)
	}
	t.Cleanup(ctrl.Close)
	return ctrl
}

func TestNewController(t *testing.T) {
	ctrl := newTestController(t)
	if ctrl == nil {
		t.Fatal("expected non-nil controller")
	}
}

func TestShiftTraffic_CancelledContext(t *testing.T) {
	ctrl := newTestController(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := ctrl.ShiftTraffic(ctx, "test-project", "asia-northeast1", "my-service", "my-revision", 10)
	if err == nil {
		t.Error("expected error with cancelled context")
	}
}

func TestExecuteJob_CancelledContext(t *testing.T) {
	ctrl := newTestController(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := ctrl.ExecuteJob(ctx, "test-project", "asia-northeast1", "my-job", []string{"--migrate"})
	if err == nil {
		t.Error("expected error with cancelled context")
	}
}

func TestTriggerBackup_CancelledContext(t *testing.T) {
	ctrl := newTestController(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := ctrl.TriggerBackup(ctx, "test-project", "my-instance")
	if err == nil {
		t.Error("expected error with cancelled context")
	}
}

func TestUpdateWorkerPool_CancelledContext(t *testing.T) {
	ctrl := newTestController(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := ctrl.UpdateWorkerPool(ctx, "test-project", "asia-northeast1", "my-pool", "my-revision", 20)
	if err == nil {
		t.Error("expected error with cancelled context")
	}
}
