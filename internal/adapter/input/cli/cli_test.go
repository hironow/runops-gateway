package cli_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/hironow/runops-gateway/internal/adapter/input/cli"
	"github.com/hironow/runops-gateway/internal/core/domain"
)

// mockUseCase is a test double for port.RunOpsUseCase.
type mockUseCase struct {
	approveErr    error
	denyErr       error
	approveCalled bool
	denyCalled    bool
	lastReq       domain.ApprovalRequest
}

func (m *mockUseCase) ApproveAction(_ context.Context, req domain.ApprovalRequest) error {
	m.approveCalled = true
	m.lastReq = req
	return m.approveErr
}

func (m *mockUseCase) DenyAction(_ context.Context, req domain.ApprovalRequest) error {
	m.denyCalled = true
	m.lastReq = req
	return m.denyErr
}

// TestApproveCmd_Success verifies ApproveAction is called and stdout contains "Successfully".
func TestApproveCmd_Success(t *testing.T) {
	// given
	mock := &mockUseCase{approveErr: nil}
	root := cli.NewRootCmd(mock)
	buf := &bytes.Buffer{}
	root.SetOut(buf)

	// when
	root.SetArgs([]string{"approve", "service", "frontend-service",
		"--project=test-project", "--location=asia-northeast1",
		"--action=canary_10", "--target=v001", "--approver=U123"})
	err := root.Execute()

	// then
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

// TestApproveCmd_MissingAction verifies that omitting --action returns an error.
func TestApproveCmd_MissingAction(t *testing.T) {
	// given
	mock := &mockUseCase{}
	root := cli.NewRootCmd(mock)
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(&bytes.Buffer{})

	// when
	root.SetArgs([]string{"approve", "service", "frontend-service",
		"--project=test-project", "--location=asia-northeast1", "--approver=U123"})
	err := root.Execute()

	// then
	if err == nil {
		t.Fatal("expected error when --action is missing, got nil")
	}
}

// TestApproveCmd_MissingArgs verifies that missing positional args returns an error.
func TestApproveCmd_MissingArgs(t *testing.T) {
	// given
	mock := &mockUseCase{}
	root := cli.NewRootCmd(mock)
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})

	// when
	root.SetArgs([]string{"approve"})
	err := root.Execute()

	// then
	if err == nil {
		t.Fatal("expected error when positional args are missing, got nil")
	}
}

// TestApproveCmd_UseCaseError verifies that a useCase error is propagated.
func TestApproveCmd_UseCaseError(t *testing.T) {
	// given
	mock := &mockUseCase{approveErr: errors.New("gcp unavailable")}
	root := cli.NewRootCmd(mock)
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})

	// when
	root.SetArgs([]string{"approve", "service", "frontend-service",
		"--project=test-project", "--location=asia-northeast1",
		"--action=canary_10", "--approver=U123"})
	err := root.Execute()

	// then
	if err == nil {
		t.Fatal("expected error from useCase, got nil")
	}
}

// TestApproveCmd_CLIMode verifies Source is "cli" and IssuedAt is 0.
func TestApproveCmd_CLIMode(t *testing.T) {
	// given
	mock := &mockUseCase{}
	root := cli.NewRootCmd(mock)
	root.SetOut(&bytes.Buffer{})

	// when
	root.SetArgs([]string{"approve", "service", "frontend-service",
		"--project=test-project", "--location=asia-northeast1",
		"--action=canary_10", "--approver=U123"})
	err := root.Execute()

	// then
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.lastReq.Project != "test-project" {
		t.Errorf("expected Project=%q, got %q", "test-project", mock.lastReq.Project)
	}
	if mock.lastReq.Location != "asia-northeast1" {
		t.Errorf("expected Location=%q, got %q", "asia-northeast1", mock.lastReq.Location)
	}
	if mock.lastReq.Source != "cli" {
		t.Errorf("expected Source=%q, got %q", "cli", mock.lastReq.Source)
	}
	if mock.lastReq.IssuedAt != 0 {
		t.Errorf("expected IssuedAt=0, got %d", mock.lastReq.IssuedAt)
	}
}

// TestDenyCmd_Success verifies DenyAction is called and stdout contains "denied".
func TestDenyCmd_Success(t *testing.T) {
	// given
	mock := &mockUseCase{}
	root := cli.NewRootCmd(mock)
	buf := &bytes.Buffer{}
	root.SetOut(buf)

	// when
	root.SetArgs([]string{"deny", "service", "frontend-service",
		"--project=test-project", "--location=asia-northeast1", "--approver=U123"})
	err := root.Execute()

	// then
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

// TestDenyCmd_MissingArgs verifies that missing positional args returns an error.
func TestDenyCmd_MissingArgs(t *testing.T) {
	// given
	mock := &mockUseCase{}
	root := cli.NewRootCmd(mock)
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})

	// when
	root.SetArgs([]string{"deny"})
	err := root.Execute()

	// then
	if err == nil {
		t.Fatal("expected error when positional args are missing, got nil")
	}
}
