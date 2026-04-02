package gcp_test

import (
	"context"
	"testing"

	"github.com/hironow/runops-gateway/internal/adapter/output/gcp"
	"github.com/hironow/runops-gateway/internal/core/port"
)

// Compile-time check: Controller must satisfy port.GCPController.
var _ port.GCPController = (*gcp.Controller)(nil)

func TestNewController_MissingProjectID(t *testing.T) {
	// given
	cfg := gcp.Config{ProjectID: ""}

	// when
	ctrl, err := gcp.NewController(cfg)

	// then
	if err == nil {
		t.Error("expected error when ProjectID is empty")
	}
	if ctrl != nil {
		t.Error("expected nil controller when ProjectID is empty")
	}
}

func TestNewController_DefaultLocation(t *testing.T) {
	// given
	cfg := gcp.Config{ProjectID: "test-project", Location: ""}

	// when
	ctrl, err := gcp.NewController(cfg)

	// then
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ctrl == nil {
		t.Fatal("expected non-nil controller")
	}
	if ctrl.Location() != "asia-northeast1" {
		t.Errorf("expected default location 'asia-northeast1', got %q", ctrl.Location())
	}
}

func TestNewController_Valid(t *testing.T) {
	// given
	cfg := gcp.Config{ProjectID: "my-project", Location: "us-central1"}

	// when
	ctrl, err := gcp.NewController(cfg)

	// then
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ctrl == nil {
		t.Fatal("expected non-nil controller")
	}
}

func TestShiftTraffic_CancelledContext(t *testing.T) {
	// given
	ctrl, _ := gcp.NewController(gcp.Config{ProjectID: "test-project"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel

	// when
	err := ctrl.ShiftTraffic(ctx, "my-service", "my-revision", 10)

	// then — should fail (either context error or GCP auth error in test env)
	if err == nil {
		t.Error("expected error with cancelled context")
	}
}

func TestExecuteJob_CancelledContext(t *testing.T) {
	// given
	ctrl, _ := gcp.NewController(gcp.Config{ProjectID: "test-project"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel

	// when
	err := ctrl.ExecuteJob(ctx, "my-job", []string{"--migrate"})

	// then — should fail (either context error or GCP auth error in test env)
	if err == nil {
		t.Error("expected error with cancelled context")
	}
}

func TestTriggerBackup_CancelledContext(t *testing.T) {
	// given
	ctrl, _ := gcp.NewController(gcp.Config{ProjectID: "test-project"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel

	// when
	err := ctrl.TriggerBackup(ctx, "my-instance")

	// then — should fail (either context error or GCP auth error in test env)
	if err == nil {
		t.Error("expected error with cancelled context")
	}
}
