package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hironow/runops-gateway/internal/core/domain"
	"github.com/hironow/runops-gateway/internal/core/port"
)

// --- mock implementations ---

type mockGCP struct {
	shiftTrafficCalled bool
	shiftTrafficErr    error
	executeJobCalled   bool
	executeJobErr      error
	triggerBackupCalled bool
	triggerBackupErr   error
}

func (m *mockGCP) ShiftTraffic(_ context.Context, _, _ string, _ int32) error {
	m.shiftTrafficCalled = true
	return m.shiftTrafficErr
}

func (m *mockGCP) ExecuteJob(_ context.Context, _ string, _ []string) error {
	m.executeJobCalled = true
	return m.executeJobErr
}

func (m *mockGCP) TriggerBackup(_ context.Context, _ string) error {
	m.triggerBackupCalled = true
	return m.triggerBackupErr
}

type mockNotifier struct {
	updateMessageCalled  bool
	updateMessageErr     error
	replaceMessageCalled bool
	replaceMessageBlocks any
	sendEphemeralCalled  bool
	sendEphemeralText    string
}

func (m *mockNotifier) UpdateMessage(_ context.Context, _ port.NotifyTarget, _ string) error {
	m.updateMessageCalled = true
	return m.updateMessageErr
}

func (m *mockNotifier) ReplaceMessage(_ context.Context, _ port.NotifyTarget, blocks any) error {
	m.replaceMessageCalled = true
	m.replaceMessageBlocks = blocks
	return nil
}

func (m *mockNotifier) SendEphemeral(_ context.Context, _ port.NotifyTarget, _ string, text string) error {
	m.sendEphemeralCalled = true
	m.sendEphemeralText = text
	return nil
}

type mockAuth struct {
	authorized bool
	expired    bool
}

func (m *mockAuth) IsAuthorized(_ string) bool { return m.authorized }
func (m *mockAuth) IsExpired(_ int64) bool      { return m.expired }

// --- helpers ---

func newServiceReq() domain.ApprovalRequest {
	return domain.ApprovalRequest{
		ResourceType: domain.ResourceTypeService,
		ResourceName: "frontend-service",
		Target:       "frontend-service-v2",
		Action:       "canary_10",
		ApproverID:   "U123",
		Source:       "slack",
		IssuedAt:     time.Now().Unix(),
		ResponseURL:  "https://hooks.slack.com/xxx",
	}
}

func newJobReq() domain.ApprovalRequest {
	return domain.ApprovalRequest{
		ResourceType: domain.ResourceTypeJob,
		ResourceName: "migration-job",
		Target:       "",
		Action:       "migrate_apply",
		ApproverID:   "U123",
		Source:       "slack",
		IssuedAt:     time.Now().Unix(),
		ResponseURL:  "https://hooks.slack.com/xxx",
	}
}

func newWorkerPoolReq() domain.ApprovalRequest {
	return domain.ApprovalRequest{
		ResourceType: domain.ResourceTypeWorkerPool,
		ResourceName: "batch-pool",
		Target:       "batch-pool-v2",
		Action:       "canary_20",
		ApproverID:   "U123",
		Source:       "slack",
		IssuedAt:     time.Now().Unix(),
		ResponseURL:  "https://hooks.slack.com/xxx",
	}
}

// --- tests ---

func TestApproveAction_Service_Success(t *testing.T) {
	// given
	gcp := &mockGCP{}
	notifier := &mockNotifier{}
	auth := &mockAuth{authorized: true, expired: false}
	svc := NewRunOpsService(gcp, notifier, auth)
	req := newServiceReq()

	// when
	err := svc.ApproveAction(context.Background(), req)

	// then
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if !gcp.shiftTrafficCalled {
		t.Error("expected ShiftTraffic to be called")
	}
	if !notifier.replaceMessageCalled {
		t.Error("expected ReplaceMessage to be called")
	}
}

func TestApproveAction_Job_Success(t *testing.T) {
	// given
	gcp := &mockGCP{}
	notifier := &mockNotifier{}
	auth := &mockAuth{authorized: true, expired: false}
	svc := NewRunOpsService(gcp, notifier, auth)
	req := newJobReq()

	// when
	err := svc.ApproveAction(context.Background(), req)

	// then
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if !gcp.triggerBackupCalled {
		t.Error("expected TriggerBackup to be called")
	}
	if !gcp.executeJobCalled {
		t.Error("expected ExecuteJob to be called")
	}
	if !notifier.replaceMessageCalled {
		t.Error("expected ReplaceMessage to be called")
	}
}

func TestApproveAction_WorkerPool_Success(t *testing.T) {
	// given
	gcp := &mockGCP{}
	notifier := &mockNotifier{}
	auth := &mockAuth{authorized: true, expired: false}
	svc := NewRunOpsService(gcp, notifier, auth)
	req := newWorkerPoolReq()

	// when
	err := svc.ApproveAction(context.Background(), req)

	// then
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if !gcp.shiftTrafficCalled {
		t.Error("expected ShiftTraffic to be called")
	}
	if !notifier.replaceMessageCalled {
		t.Error("expected ReplaceMessage to be called")
	}
}

func TestApproveAction_UnauthorizedUser(t *testing.T) {
	// given
	gcp := &mockGCP{}
	notifier := &mockNotifier{}
	auth := &mockAuth{authorized: false, expired: false}
	svc := NewRunOpsService(gcp, notifier, auth)
	req := newServiceReq()

	// when
	err := svc.ApproveAction(context.Background(), req)

	// then
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if !notifier.sendEphemeralCalled {
		t.Error("expected SendEphemeral to be called")
	}
	if gcp.shiftTrafficCalled {
		t.Error("expected ShiftTraffic NOT to be called")
	}
}

func TestApproveAction_ExpiredButton(t *testing.T) {
	// given
	gcp := &mockGCP{}
	notifier := &mockNotifier{}
	auth := &mockAuth{authorized: true, expired: true}
	svc := NewRunOpsService(gcp, notifier, auth)
	req := newServiceReq()

	// when
	err := svc.ApproveAction(context.Background(), req)

	// then
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if !notifier.sendEphemeralCalled {
		t.Error("expected SendEphemeral to be called")
	}
	if gcp.shiftTrafficCalled {
		t.Error("expected ShiftTraffic NOT to be called")
	}
}

func TestApproveAction_UnknownResourceType(t *testing.T) {
	// given
	gcp := &mockGCP{}
	notifier := &mockNotifier{}
	auth := &mockAuth{authorized: true, expired: false}
	svc := NewRunOpsService(gcp, notifier, auth)
	req := newServiceReq()
	req.ResourceType = "unknown"

	// when
	err := svc.ApproveAction(context.Background(), req)

	// then
	if err == nil {
		t.Fatal("expected error for unknown resource type, got nil")
	}
}

func TestApproveAction_GCPError_Service(t *testing.T) {
	// given
	gcp := &mockGCP{shiftTrafficErr: errors.New("gcp error")}
	notifier := &mockNotifier{}
	auth := &mockAuth{authorized: true, expired: false}
	svc := NewRunOpsService(gcp, notifier, auth)
	req := newServiceReq()

	// when
	err := svc.ApproveAction(context.Background(), req)

	// then
	if err == nil {
		t.Fatal("expected error when GCP fails, got nil")
	}
}

func TestApproveAction_NotifierError_DoesNotBlock(t *testing.T) {
	// given
	gcp := &mockGCP{}
	notifier := &mockNotifier{updateMessageErr: errors.New("slack error")}
	auth := &mockAuth{authorized: true, expired: false}
	svc := NewRunOpsService(gcp, notifier, auth)
	req := newServiceReq()

	// when
	err := svc.ApproveAction(context.Background(), req)

	// then
	if err != nil {
		t.Fatalf("notifier error should not block execution, got %v", err)
	}
	if !gcp.shiftTrafficCalled {
		t.Error("expected ShiftTraffic to be called despite notifier error")
	}
}

func TestDenyAction_Success(t *testing.T) {
	// given
	gcp := &mockGCP{}
	notifier := &mockNotifier{}
	auth := &mockAuth{authorized: true, expired: false}
	svc := NewRunOpsService(gcp, notifier, auth)
	req := newServiceReq()

	// when
	err := svc.DenyAction(context.Background(), req)

	// then
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if !notifier.replaceMessageCalled {
		t.Error("expected ReplaceMessage to be called")
	}
}

func TestApproveAction_CLIMode(t *testing.T) {
	// given
	gcp := &mockGCP{}
	notifier := &mockNotifier{}
	auth := &mockAuth{authorized: true, expired: false}
	svc := NewRunOpsService(gcp, notifier, auth)
	req := newServiceReq()
	req.Source = "cli"

	// when — we just verify no panic and mode logic is exercised
	err := svc.ApproveAction(context.Background(), req)

	// then
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if !gcp.shiftTrafficCalled {
		t.Error("expected ShiftTraffic to be called")
	}
}
