package cli_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/hironow/runops-gateway/internal/adapter/input/cli"
	"github.com/hironow/runops-gateway/internal/core/domain"
	"github.com/hironow/runops-gateway/internal/core/port"
)

type mockUseCase struct {
	approveErr    error
	denyErr       error
	approveCalled bool
	denyCalled    bool
	lastReq       domain.ApprovalRequest
	lastTarget    port.NotifyTarget
}

func (m *mockUseCase) ApproveAction(_ context.Context, req domain.ApprovalRequest, target port.NotifyTarget) error {
	m.approveCalled = true
	m.lastReq = req
	m.lastTarget = target
	return m.approveErr
}

func (m *mockUseCase) DenyAction(_ context.Context, req domain.ApprovalRequest, target port.NotifyTarget) error {
	m.denyCalled = true
	m.lastReq = req
	m.lastTarget = target
	return m.denyErr
}

func TestApproveCmd_Success(t *testing.T) {
	mock := &mockUseCase{approveErr: nil}
	root := cli.NewRootCmd(mock)
	buf := &bytes.Buffer{}
	root.SetOut(buf)

	root.SetArgs([]string{"approve", "service", "frontend-service",
		"--project=test-project", "--location=asia-northeast1",
		"--action=canary_10", "--target=v001", "--approver=U123"})
	err := root.Execute()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !mock.approveCalled {
		t.Error("ApproveAction was not called")
	}
	if !strings.Contains(buf.String(), "Successfully") {
		t.Errorf("unexpected output: %q", buf.String())
	}
}

func TestApproveCmd_MissingAction(t *testing.T) {
	mock := &mockUseCase{}
	root := cli.NewRootCmd(mock)
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})

	root.SetArgs([]string{"approve", "service", "frontend-service",
		"--project=test-project", "--location=asia-northeast1", "--approver=U123"})
	err := root.Execute()

	if err == nil {
		t.Fatal("expected error when --action is missing, got nil")
	}
}

func TestApproveCmd_MissingArgs(t *testing.T) {
	mock := &mockUseCase{}
	root := cli.NewRootCmd(mock)
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})

	root.SetArgs([]string{"approve"})
	err := root.Execute()

	if err == nil {
		t.Fatal("expected error when positional args are missing, got nil")
	}
}

func TestApproveCmd_UseCaseError(t *testing.T) {
	mock := &mockUseCase{approveErr: errors.New("gcp unavailable")}
	root := cli.NewRootCmd(mock)
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})

	root.SetArgs([]string{"approve", "service", "frontend-service",
		"--project=test-project", "--location=asia-northeast1",
		"--action=canary_10", "--approver=U123"})
	err := root.Execute()

	if err == nil {
		t.Fatal("expected error from useCase, got nil")
	}
}

func TestApproveCmd_CLIMode_StdoutTarget(t *testing.T) {
	mock := &mockUseCase{}
	root := cli.NewRootCmd(mock)
	root.SetOut(&bytes.Buffer{})

	root.SetArgs([]string{"approve", "service", "frontend-service",
		"--project=test-project", "--location=asia-northeast1",
		"--action=canary_10", "--approver=U123"})
	err := root.Execute()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.lastTarget.Mode != port.ModeStdout {
		t.Errorf("expected Mode=%q, got %q", port.ModeStdout, mock.lastTarget.Mode)
	}
	if mock.lastReq.IssuedAt != 0 {
		t.Errorf("expected IssuedAt=0, got %d", mock.lastReq.IssuedAt)
	}
}

func TestDenyCmd_Success(t *testing.T) {
	mock := &mockUseCase{}
	root := cli.NewRootCmd(mock)
	buf := &bytes.Buffer{}
	root.SetOut(buf)

	root.SetArgs([]string{"deny", "service", "frontend-service",
		"--project=test-project", "--location=asia-northeast1", "--approver=U123"})
	err := root.Execute()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !mock.denyCalled {
		t.Error("DenyAction was not called")
	}
	if !strings.Contains(buf.String(), "denied") {
		t.Errorf("unexpected output: %q", buf.String())
	}
}

func TestDenyCmd_MissingArgs(t *testing.T) {
	mock := &mockUseCase{}
	root := cli.NewRootCmd(mock)
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})

	root.SetArgs([]string{"deny"})
	err := root.Execute()

	if err == nil {
		t.Fatal("expected error when positional args are missing, got nil")
	}
}
