package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hironow/runops-gateway/internal/adapter/output/state"
	"github.com/hironow/runops-gateway/internal/core/domain"
	"github.com/hironow/runops-gateway/internal/core/port"
)

// --- mock implementations ---

type mockGCP struct {
	shiftTrafficCalled     bool
	shiftTrafficPercent    int32
	shiftTrafficErr        error
	executeJobCalled       bool
	executeJobErr          error
	triggerBackupCalled    bool
	triggerBackupErr       error
	updateWorkerPoolCalled bool
	updateWorkerPoolErr    error
}

func (m *mockGCP) ShiftTraffic(_ context.Context, _, _ string, percent int32) error {
	m.shiftTrafficCalled = true
	m.shiftTrafficPercent = percent
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

func (m *mockGCP) UpdateWorkerPool(_ context.Context, _, _ string, _ int32) error {
	m.updateWorkerPoolCalled = true
	return m.updateWorkerPoolErr
}

type mockNotifier struct {
	updateMessageCalled     bool
	updateMessageErr        error
	replaceMessageCalled    bool
	replaceMessageBlocks    any
	replaceErr              error
	sendEphemeralCalled     bool
	sendEphemeralText       string
	offerContinuationCalled bool
	offerContinuationNextReq *domain.ApprovalRequest
	offerContinuationErr    error
}

func (m *mockNotifier) UpdateMessage(_ context.Context, _ port.NotifyTarget, _ string) error {
	m.updateMessageCalled = true
	return m.updateMessageErr
}

func (m *mockNotifier) ReplaceMessage(_ context.Context, _ port.NotifyTarget, blocks any) error {
	m.replaceMessageCalled = true
	m.replaceMessageBlocks = blocks
	return m.replaceErr
}

func (m *mockNotifier) SendEphemeral(_ context.Context, _ port.NotifyTarget, _ string, text string) error {
	m.sendEphemeralCalled = true
	m.sendEphemeralText = text
	return nil
}

func (m *mockNotifier) OfferContinuation(_ context.Context, _ port.NotifyTarget, _ string, nextReq *domain.ApprovalRequest, _ *domain.ApprovalRequest) error {
	m.offerContinuationCalled = true
	m.offerContinuationNextReq = nextReq
	return m.offerContinuationErr
}

type mockAuth struct {
	authorized bool
	expired    bool
}

func (m *mockAuth) IsAuthorized(_ string) bool { return m.authorized }
func (m *mockAuth) IsExpired(_ int64) bool     { return m.expired }

type mockStore struct{ locked bool }

func (m *mockStore) TryLock(_ string) bool { m.locked = true; return true }
func (m *mockStore) Release(_ string)      { m.locked = false }

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
	svc := NewRunOpsService(gcp, notifier, auth, &mockStore{})
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
	if !notifier.offerContinuationCalled {
		t.Error("expected OfferContinuation to be called")
	}
}

func TestApproveAction_Job_Success(t *testing.T) {
	// given
	gcp := &mockGCP{}
	notifier := &mockNotifier{}
	auth := &mockAuth{authorized: true, expired: false}
	svc := NewRunOpsService(gcp, notifier, auth, &mockStore{})
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
	svc := NewRunOpsService(gcp, notifier, auth, &mockStore{})
	req := newWorkerPoolReq()

	// when
	err := svc.ApproveAction(context.Background(), req)

	// then
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if !gcp.updateWorkerPoolCalled {
		t.Error("expected UpdateWorkerPool to be called")
	}
	if gcp.shiftTrafficCalled {
		t.Error("expected ShiftTraffic NOT to be called for worker pool")
	}
	if !notifier.offerContinuationCalled {
		t.Error("expected OfferContinuation to be called")
	}
}

func TestApproveAction_UnauthorizedUser(t *testing.T) {
	// given
	gcp := &mockGCP{}
	notifier := &mockNotifier{}
	auth := &mockAuth{authorized: false, expired: false}
	svc := NewRunOpsService(gcp, notifier, auth, &mockStore{})
	req := newServiceReq()

	// when
	err := svc.ApproveAction(context.Background(), req)

	// then — must return non-nil so CLI callers get a non-zero exit code
	if err == nil {
		t.Fatal("expected error for unauthorized user, got nil")
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
	svc := NewRunOpsService(gcp, notifier, auth, &mockStore{})
	req := newServiceReq()

	// when
	err := svc.ApproveAction(context.Background(), req)

	// then — must return non-nil so CLI callers get a non-zero exit code
	if err == nil {
		t.Fatal("expected error for expired request, got nil")
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
	svc := NewRunOpsService(gcp, notifier, auth, &mockStore{})
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
	svc := NewRunOpsService(gcp, notifier, auth, &mockStore{})
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
	svc := NewRunOpsService(gcp, notifier, auth, &mockStore{})
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
	svc := NewRunOpsService(gcp, notifier, auth, &mockStore{})
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
	svc := NewRunOpsService(gcp, notifier, auth, &mockStore{})
	req := newServiceReq()
	req.Source = "cli"

	// when — verify no panic and mode logic is exercised
	err := svc.ApproveAction(context.Background(), req)

	// then
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if !gcp.shiftTrafficCalled {
		t.Error("expected ShiftTraffic to be called")
	}
	if !notifier.offerContinuationCalled {
		t.Error("expected OfferContinuation to be called")
	}
}

func TestApproveAction_Service_InvalidAction(t *testing.T) {
	// given — action string that produces a ParseAction error (percent > 100)
	gcp := &mockGCP{}
	notifier := &mockNotifier{}
	auth := &mockAuth{authorized: true, expired: false}
	svc := NewRunOpsService(gcp, notifier, auth, &mockStore{})
	req := newServiceReq()
	req.Action = "canary_101" // invalid: percent > 100

	// when
	err := svc.ApproveAction(context.Background(), req)

	// then — error returned, ShiftTraffic never called
	if err == nil {
		t.Fatal("expected error for invalid action, got nil")
	}
	if gcp.shiftTrafficCalled {
		t.Error("expected ShiftTraffic NOT to be called after parse error")
	}
}

func TestApproveAction_WorkerPool_InvalidAction(t *testing.T) {
	// given — action string that produces a ParseAction error
	gcp := &mockGCP{}
	notifier := &mockNotifier{}
	auth := &mockAuth{authorized: true, expired: false}
	svc := NewRunOpsService(gcp, notifier, auth, &mockStore{})
	req := newWorkerPoolReq()
	req.Action = "canary_-1" // invalid: negative percent

	// when
	err := svc.ApproveAction(context.Background(), req)

	// then
	if err == nil {
		t.Fatal("expected error for invalid action, got nil")
	}
	if gcp.updateWorkerPoolCalled {
		t.Error("expected UpdateWorkerPool NOT to be called after parse error")
	}
}

func TestApproveAction_Job_TriggerBackupError(t *testing.T) {
	// given — TriggerBackup fails; ExecuteJob must not be called
	gcp := &mockGCP{triggerBackupErr: errors.New("backup failed")}
	notifier := &mockNotifier{}
	auth := &mockAuth{authorized: true, expired: false}
	svc := NewRunOpsService(gcp, notifier, auth, &mockStore{})
	req := newJobReq()

	// when
	err := svc.ApproveAction(context.Background(), req)

	// then
	if err == nil {
		t.Fatal("expected error when TriggerBackup fails, got nil")
	}
	if gcp.executeJobCalled {
		t.Error("expected ExecuteJob NOT to be called when backup fails")
	}
}

func TestApproveAction_Job_ExecuteJobError(t *testing.T) {
	// given — backup succeeds but migration job fails
	gcp := &mockGCP{executeJobErr: errors.New("job failed")}
	notifier := &mockNotifier{}
	auth := &mockAuth{authorized: true, expired: false}
	svc := NewRunOpsService(gcp, notifier, auth, &mockStore{})
	req := newJobReq()

	// when
	err := svc.ApproveAction(context.Background(), req)

	// then — error propagated, backup was called
	if err == nil {
		t.Fatal("expected error when ExecuteJob fails, got nil")
	}
	if !gcp.triggerBackupCalled {
		t.Error("expected TriggerBackup to have been called before job failure")
	}
}

func TestApproveAction_Service_RollbackShiftsToZero(t *testing.T) {
	// given — rollback action must shift traffic to 0% (NOT default to 10)
	gcp := &mockGCP{}
	notifier := &mockNotifier{}
	auth := &mockAuth{authorized: true, expired: false}
	svc := NewRunOpsService(gcp, notifier, auth, &mockStore{})
	req := newServiceReq()
	req.Action = "rollback"

	// when
	err := svc.ApproveAction(context.Background(), req)

	// then
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if !gcp.shiftTrafficCalled {
		t.Error("expected ShiftTraffic to be called")
	}
	if gcp.shiftTrafficPercent != 0 {
		t.Errorf("expected ShiftTraffic percent=0 for rollback, got %d", gcp.shiftTrafficPercent)
	}
}

func TestApproveAction_Service_CanaryProgressionOffersNextStep(t *testing.T) {
	// given — canary_10 success should offer canary_30 as next step
	gcp := &mockGCP{}
	notifier := &mockNotifier{}
	auth := &mockAuth{authorized: true, expired: false}
	svc := NewRunOpsService(gcp, notifier, auth, &mockStore{})
	req := newServiceReq()
	req.Action = "canary_10"

	// when
	err := svc.ApproveAction(context.Background(), req)

	// then
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if !notifier.offerContinuationCalled {
		t.Error("expected OfferContinuation to be called after canary step")
	}
	if notifier.offerContinuationNextReq == nil {
		t.Fatal("expected nextReq to be non-nil after canary_10")
	}
	if notifier.offerContinuationNextReq.Action != "canary_30" {
		t.Errorf("expected next action canary_30, got %s", notifier.offerContinuationNextReq.Action)
	}
}

func TestApproveAction_Service_Canary100OffersNoNextStep(t *testing.T) {
	// given — canary_100 is the final step; OfferContinuation called with nil nextReq
	gcp := &mockGCP{}
	notifier := &mockNotifier{}
	auth := &mockAuth{authorized: true, expired: false}
	svc := NewRunOpsService(gcp, notifier, auth, &mockStore{})
	req := newServiceReq()
	req.Action = "canary_100"

	// when
	err := svc.ApproveAction(context.Background(), req)

	// then
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if !notifier.offerContinuationCalled {
		t.Error("expected OfferContinuation to be called")
	}
	if notifier.offerContinuationNextReq != nil {
		t.Errorf("expected nextReq nil for final canary step, got %+v", notifier.offerContinuationNextReq)
	}
}

func TestApproveAction_Job_WithNextService_OffersCanaryButton(t *testing.T) {
	// given — job request with next_* fields set; after migration, canary button should be offered
	gcp := &mockGCP{}
	notifier := &mockNotifier{}
	auth := &mockAuth{authorized: true, expired: false}
	svc := NewRunOpsService(gcp, notifier, auth, &mockStore{})
	req := newJobReq()
	req.NextServiceName = "frontend-service"
	req.NextRevision = "frontend-service-v2"
	req.NextAction = "canary_10"

	// when
	err := svc.ApproveAction(context.Background(), req)

	// then
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if !notifier.offerContinuationCalled {
		t.Error("expected OfferContinuation to be called after migration with NextServiceName set")
	}
	if notifier.offerContinuationNextReq == nil {
		t.Fatal("expected nextReq to be non-nil")
	}
	if notifier.offerContinuationNextReq.ResourceName != "frontend-service" {
		t.Errorf("expected nextReq.ResourceName=frontend-service, got %s", notifier.offerContinuationNextReq.ResourceName)
	}
	if !notifier.offerContinuationNextReq.MigrationDone {
		t.Error("expected nextReq.MigrationDone=true")
	}
}

func TestApproveAction_UnauthorizedTakesPriorityOverExpired(t *testing.T) {
	// given — both unauthorized and expired; unauthorized check runs first
	gcp := &mockGCP{}
	notifier := &mockNotifier{}
	auth := &mockAuth{authorized: false, expired: true}
	svc := NewRunOpsService(gcp, notifier, auth, &mockStore{})
	req := newServiceReq()

	// when
	err := svc.ApproveAction(context.Background(), req)

	// then — error returned (unauthorized path takes priority)
	if err == nil {
		t.Fatal("expected error for unauthorized user, got nil")
	}
	if !notifier.sendEphemeralCalled {
		t.Error("expected SendEphemeral to be called")
	}
	if gcp.shiftTrafficCalled {
		t.Error("expected ShiftTraffic NOT to be called")
	}
}

func TestDenyAction_NotifierError_ReturnsError(t *testing.T) {
	// given — ReplaceMessage fails; DenyAction's only job is notification, so failure must propagate
	gcp := &mockGCP{}
	notifier := &mockNotifier{}
	notifier.replaceErr = errors.New("slack down")
	auth := &mockAuth{authorized: true, expired: false}
	svc := NewRunOpsService(gcp, notifier, auth, &mockStore{})
	req := newServiceReq()

	// when
	err := svc.DenyAction(context.Background(), req)

	// then — notification IS the operation; its failure is a real failure
	if err == nil {
		t.Fatal("expected error when denial notification fails, got nil")
	}
}

func TestApproveAction_DuplicateExecution(t *testing.T) {
	// given — use a real MemoryStore so the lock persists across calls
	gcp := &mockGCP{}
	notifier := &mockNotifier{}
	auth := &mockAuth{authorized: true, expired: false}
	store := state.NewMemoryStore()
	svc := NewRunOpsService(gcp, notifier, auth, store)
	req := newServiceReq()

	// when — first call succeeds
	err := svc.ApproveAction(context.Background(), req)

	// then — first call should have run successfully
	if err != nil {
		t.Fatalf("expected nil error on first call, got %v", err)
	}

	// Lock is released after first call (via defer), so lock again manually to simulate in-flight
	key := port.OperationKey(req)
	store.TryLock(key) // simulate an in-flight operation

	// Reset GCP mock to detect if second call invokes ShiftTraffic
	gcp.shiftTrafficCalled = false

	// when — second call finds the key locked
	err2 := svc.ApproveAction(context.Background(), req)

	// then — second call should return non-nil (rejected) and NOT call ShiftTraffic
	if err2 == nil {
		t.Fatal("expected error on duplicate execution, got nil")
	}
	if gcp.shiftTrafficCalled {
		t.Error("expected ShiftTraffic NOT to be called on duplicate execution")
	}
}
