package port_test

import (
	"context"
	"testing"

	"github.com/hironow/runops-gateway/internal/core/domain"
	"github.com/hironow/runops-gateway/internal/core/port"
)

// Compile-time interface assertions — these will fail to compile if the
// interfaces are not satisfied, which is the desired Red-phase failure.

// stubUseCase implements RunOpsUseCase.
type stubUseCase struct{}

func (s *stubUseCase) ApproveAction(_ context.Context, _ domain.ApprovalRequest) error {
	return nil
}

func (s *stubUseCase) DenyAction(_ context.Context, _ domain.ApprovalRequest) error {
	return nil
}

var _ port.RunOpsUseCase = (*stubUseCase)(nil)

// stubGCPController implements GCPController.
type stubGCPController struct{}

func (s *stubGCPController) ShiftTraffic(_ context.Context, _, _ string, _ int32) error {
	return nil
}

func (s *stubGCPController) ExecuteJob(_ context.Context, _ string, _ []string) error {
	return nil
}

func (s *stubGCPController) TriggerBackup(_ context.Context, _ string) error {
	return nil
}

var _ port.GCPController = (*stubGCPController)(nil)

// stubNotifier implements Notifier.
type stubNotifier struct{}

func (s *stubNotifier) UpdateMessage(_ context.Context, _ port.NotifyTarget, _ string) error {
	return nil
}

func (s *stubNotifier) ReplaceMessage(_ context.Context, _ port.NotifyTarget, _ any) error {
	return nil
}

func (s *stubNotifier) SendEphemeral(_ context.Context, _ port.NotifyTarget, _, _ string) error {
	return nil
}

var _ port.Notifier = (*stubNotifier)(nil)

// stubAuthChecker implements AuthChecker.
type stubAuthChecker struct{}

func (s *stubAuthChecker) IsAuthorized(_ string) bool { return true }
func (s *stubAuthChecker) IsExpired(_ int64) bool      { return false }

var _ port.AuthChecker = (*stubAuthChecker)(nil)

func TestNotifyTargetSlackMode(t *testing.T) {
	// given
	target := port.NotifyTarget{
		ResponseURL: "https://hooks.slack.com/actions/xxx",
		Mode:        "slack",
	}

	// when / then
	if target.Mode != "slack" {
		t.Errorf("Mode = %q, want %q", target.Mode, "slack")
	}
	if target.ResponseURL == "" {
		t.Error("Slack mode ResponseURL must not be empty")
	}
}

func TestNotifyTargetStdoutMode(t *testing.T) {
	// given
	target := port.NotifyTarget{
		ResponseURL: "",
		Mode:        "stdout",
	}

	// when / then
	if target.Mode != "stdout" {
		t.Errorf("Mode = %q, want %q", target.Mode, "stdout")
	}
	if target.ResponseURL != "" {
		t.Errorf("stdout mode ResponseURL should be empty, got %q", target.ResponseURL)
	}
}
