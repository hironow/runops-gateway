package gcp_test

import (
	"context"
	"testing"

	"github.com/hironow/runops-gateway/internal/adapter/output/gcp"
	"github.com/hironow/runops-gateway/internal/core/port"
)

// Compile-time check: Controller must satisfy port.GCPController.
var _ port.GCPController = (*gcp.Controller)(nil)

func TestNewController(t *testing.T) {
	// given / when
	ctrl := gcp.NewController()

	// then
	if ctrl == nil {
		t.Fatal("expected non-nil controller")
	}
}

func TestShiftTraffic_CancelledContext(t *testing.T) {
	// given
	ctrl := gcp.NewController()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel

	// when
	err := ctrl.ShiftTraffic(ctx, "test-project", "asia-northeast1", "my-service", "my-revision", 10)

	// then — should fail (either context error or GCP auth error in test env)
	if err == nil {
		t.Error("expected error with cancelled context")
	}
}

func TestExecuteJob_CancelledContext(t *testing.T) {
	// given
	ctrl := gcp.NewController()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel

	// when
	err := ctrl.ExecuteJob(ctx, "test-project", "asia-northeast1", "my-job", []string{"--migrate"})

	// then — should fail (either context error or GCP auth error in test env)
	if err == nil {
		t.Error("expected error with cancelled context")
	}
}

func TestTriggerBackup_CancelledContext(t *testing.T) {
	// given
	ctrl := gcp.NewController()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel

	// when
	err := ctrl.TriggerBackup(ctx, "test-project", "my-instance")

	// then — should fail (either context error or GCP auth error in test env)
	if err == nil {
		t.Error("expected error with cancelled context")
	}
}

func TestUpdateWorkerPool_CancelledContext(t *testing.T) {
	// given
	ctrl := gcp.NewController()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel

	// when
	err := ctrl.UpdateWorkerPool(ctx, "test-project", "asia-northeast1", "my-pool", "my-revision", 20)

	// then — should fail (either context error or GCP auth error in test env)
	if err == nil {
		t.Error("expected error with cancelled context")
	}
}
