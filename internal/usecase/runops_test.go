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

type shiftCall struct {
	name    string
	target  string
	percent int32
}

type workerPoolCall struct {
	name    string
	target  string
	percent int32
}

type mockGCP struct {
	shiftCalls              []shiftCall // all recorded ShiftTraffic calls in order
	shiftTrafficCalled      bool
	shiftTrafficPercent     int32
	shiftErrOnIdx           int   // if >= 0, return shiftTrafficErr on this call index
	shiftTrafficErr         error
	executeJobCalled        bool
	executeJobErr           error
	triggerBackupCalled     bool
	triggerBackupErr        error
	updateWorkerPoolCalled  bool
	updateWorkerPoolErr     error
	workerPoolCalls         []workerPoolCall // all recorded UpdateWorkerPool calls in order
	workerPoolErrOnIdx      int              // if >= 0, return updateWorkerPoolErr on this call index
}

func newMockGCP() *mockGCP { return &mockGCP{shiftErrOnIdx: -1, workerPoolErrOnIdx: -1} }

func (m *mockGCP) ShiftTraffic(_ context.Context, name, target string, percent int32) error {
	idx := len(m.shiftCalls)
	m.shiftCalls = append(m.shiftCalls, shiftCall{name, target, percent})
	m.shiftTrafficCalled = true
	m.shiftTrafficPercent = percent
	if m.shiftErrOnIdx >= 0 && idx == m.shiftErrOnIdx {
		return m.shiftTrafficErr
	}
	return nil
}

func (m *mockGCP) ExecuteJob(_ context.Context, _ string, _ []string) error {
	m.executeJobCalled = true
	return m.executeJobErr
}

func (m *mockGCP) TriggerBackup(_ context.Context, _ string) error {
	m.triggerBackupCalled = true
	return m.triggerBackupErr
}

func (m *mockGCP) UpdateWorkerPool(_ context.Context, name, target string, percent int32) error {
	idx := len(m.workerPoolCalls)
	m.workerPoolCalls = append(m.workerPoolCalls, workerPoolCall{name, target, percent})
	m.updateWorkerPoolCalled = true
	if m.workerPoolErrOnIdx >= 0 && idx == m.workerPoolErrOnIdx {
		return m.updateWorkerPoolErr
	}
	return nil
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
		ResourceType:  domain.ResourceTypeService,
		ResourceNames: "frontend-service",
		Targets:       "frontend-service-v2",
		Action:        "canary_10",
		ApproverID:    "U123",
		Source:        "slack",
		IssuedAt:      time.Now().Unix(),
		ResponseURL:   "https://hooks.slack.com/xxx",
	}
}

func newJobReq() domain.ApprovalRequest {
	return domain.ApprovalRequest{
		ResourceType:  domain.ResourceTypeJob,
		ResourceNames: "migration-job",
		Targets:       "",
		Action:        "migrate_apply",
		ApproverID:    "U123",
		Source:        "slack",
		IssuedAt:      time.Now().Unix(),
		ResponseURL:   "https://hooks.slack.com/xxx",
	}
}

func newWorkerPoolReq() domain.ApprovalRequest {
	return domain.ApprovalRequest{
		ResourceType:  domain.ResourceTypeWorkerPool,
		ResourceNames: "batch-pool",
		Targets:       "batch-pool-v2",
		Action:        "canary_20",
		ApproverID:    "U123",
		Source:        "slack",
		IssuedAt:      time.Now().Unix(),
		ResponseURL:   "https://hooks.slack.com/xxx",
	}
}

// --- tests ---

func TestApproveAction_Service_Success(t *testing.T) {
	// given
	gcp := newMockGCP()
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
	gcp := newMockGCP()
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
	gcp := newMockGCP()
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
	gcp := newMockGCP()
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
	gcp := newMockGCP()
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
	gcp := newMockGCP()
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
	gcp := &mockGCP{shiftErrOnIdx: 0, shiftTrafficErr: errors.New("gcp error")}
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
	gcp := newMockGCP()
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
	gcp := newMockGCP()
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
	// blocks must be a slice (not a map with nested replace_original/blocks)
	blocks, ok := notifier.replaceMessageBlocks.([]map[string]any)
	if !ok {
		t.Fatalf("expected blocks to be []map[string]any, got %T", notifier.replaceMessageBlocks)
	}
	if len(blocks) == 0 {
		t.Fatal("expected non-empty blocks array")
	}
	if blocks[0]["type"] != "section" {
		t.Errorf("expected first block type to be section, got %v", blocks[0]["type"])
	}
}

func TestApproveAction_CLIMode(t *testing.T) {
	// given
	gcp := newMockGCP()
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
	gcp := newMockGCP()
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
	gcp := newMockGCP()
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
	gcp := newMockGCP()
	gcp.triggerBackupErr = errors.New("backup failed")
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
	gcp := newMockGCP()
	gcp.executeJobErr = errors.New("job failed")
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
	gcp := newMockGCP()
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
	gcp := newMockGCP()
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
	gcp := newMockGCP()
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
	gcp := newMockGCP()
	notifier := &mockNotifier{}
	auth := &mockAuth{authorized: true, expired: false}
	svc := NewRunOpsService(gcp, notifier, auth, &mockStore{})
	req := newJobReq()
	req.NextServiceNames = "frontend-service"
	req.NextRevisions = "frontend-service-v2"
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
	if notifier.offerContinuationNextReq.ResourceNames != "frontend-service" {
		t.Errorf("expected nextReq.ResourceNames=frontend-service, got %s", notifier.offerContinuationNextReq.ResourceNames)
	}
	if !notifier.offerContinuationNextReq.MigrationDone {
		t.Error("expected nextReq.MigrationDone=true")
	}
}

func TestApproveAction_UnauthorizedTakesPriorityOverExpired(t *testing.T) {
	// given — both unauthorized and expired; unauthorized check runs first
	gcp := newMockGCP()
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
	gcp := newMockGCP()
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
	gcp := newMockGCP()
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

func TestApproveAction_MultiService_AllSucceed(t *testing.T) {
	// given — two services in one canary request; both must be shifted
	gcp := newMockGCP()
	notifier := &mockNotifier{}
	auth := &mockAuth{authorized: true, expired: false}
	svc := NewRunOpsService(gcp, notifier, auth, &mockStore{})
	req := newServiceReq()
	req.ResourceNames = "frontend-service,backend-service"
	req.Targets = "frontend-v2,backend-v2"
	req.Action = "canary_10"

	// when
	err := svc.ApproveAction(context.Background(), req)

	// then
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if len(gcp.shiftCalls) != 2 {
		t.Fatalf("expected 2 ShiftTraffic calls, got %d", len(gcp.shiftCalls))
	}
	if gcp.shiftCalls[0].name != "frontend-service" || gcp.shiftCalls[0].percent != 10 {
		t.Errorf("first shift: got %+v, want frontend-service@10%%", gcp.shiftCalls[0])
	}
	if gcp.shiftCalls[1].name != "backend-service" || gcp.shiftCalls[1].percent != 10 {
		t.Errorf("second shift: got %+v, want backend-service@10%%", gcp.shiftCalls[1])
	}
}

func TestApproveAction_MultiService_SecondFails_CompensatesFirst(t *testing.T) {
	// given — first service succeeds, second fails; first must be rolled back to 0%
	gcp := &mockGCP{shiftErrOnIdx: 1, shiftTrafficErr: errors.New("gcp error")}
	notifier := &mockNotifier{}
	auth := &mockAuth{authorized: true, expired: false}
	svc := NewRunOpsService(gcp, notifier, auth, &mockStore{})
	req := newServiceReq()
	req.ResourceNames = "frontend-service,backend-service"
	req.Targets = "frontend-v2,backend-v2"
	req.Action = "canary_10"

	// when
	err := svc.ApproveAction(context.Background(), req)

	// then — error returned
	if err == nil {
		t.Fatal("expected error when second ShiftTraffic fails, got nil")
	}
	// three calls expected: frontend@10% (ok), backend@10% (fail), frontend@0% (rollback)
	if len(gcp.shiftCalls) != 3 {
		t.Fatalf("expected 3 ShiftTraffic calls (2 forward + 1 rollback), got %d: %+v", len(gcp.shiftCalls), gcp.shiftCalls)
	}
	rollback := gcp.shiftCalls[2]
	if rollback.name != "frontend-service" || rollback.percent != 0 {
		t.Errorf("compensating rollback: got %+v, want frontend-service@0%%", rollback)
	}
}

func TestApproveAction_Service_MismatchedTargetCount_SecondTargetEmpty(t *testing.T) {
	// given — two services but only one target (csvAt returns "" for index 1)
	gcp := newMockGCP()
	notifier := &mockNotifier{}
	auth := &mockAuth{authorized: true, expired: false}
	svc := NewRunOpsService(gcp, notifier, auth, &mockStore{})
	req := newServiceReq()
	req.ResourceNames = "frontend-service,backend-service"
	req.Targets = "frontend-v2" // only one target provided
	req.Action = "canary_10"

	// when
	err := svc.ApproveAction(context.Background(), req)

	// then — must succeed; second ShiftTraffic call uses target=""
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if len(gcp.shiftCalls) != 2 {
		t.Fatalf("expected 2 ShiftTraffic calls, got %d", len(gcp.shiftCalls))
	}
	if gcp.shiftCalls[1].target != "" {
		t.Errorf("expected empty target for second service (csvAt out-of-bounds), got %q", gcp.shiftCalls[1].target)
	}
}

func TestApproveAction_WorkerPool_Canary0_FallsBackToPercent10(t *testing.T) {
	// given — canary_0 for worker pool must also default to percent=10
	gcp := newMockGCP()
	notifier := &mockNotifier{}
	auth := &mockAuth{authorized: true, expired: false}
	svc := NewRunOpsService(gcp, notifier, auth, &mockStore{})
	req := newWorkerPoolReq()
	req.Action = "canary_0"

	// when
	err := svc.ApproveAction(context.Background(), req)

	// then
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if len(gcp.workerPoolCalls) == 0 {
		t.Fatal("expected UpdateWorkerPool to be called")
	}
	if gcp.workerPoolCalls[0].percent != 10 {
		t.Errorf("expected percent=10 for canary_0, got %d", gcp.workerPoolCalls[0].percent)
	}
}

func TestApproveAction_Job_WithNextService_OfferContinuationError_ReturnsNil(t *testing.T) {
	// given — OfferContinuation fails after migration; error is logged but not returned
	// (spec: migration completed successfully; notifier failure must not break the operation)
	gcp := newMockGCP()
	notifier := &mockNotifier{offerContinuationErr: errors.New("slack down")}
	auth := &mockAuth{authorized: true, expired: false}
	svc := NewRunOpsService(gcp, notifier, auth, &mockStore{})
	req := newJobReq()
	req.NextServiceNames = "frontend-service"
	req.NextRevisions = "frontend-service-v2"
	req.NextAction = "canary_10"

	// when
	err := svc.ApproveAction(context.Background(), req)

	// then — migration completed; notifier error must not propagate
	if err != nil {
		t.Fatalf("OfferContinuation failure must not block migration success, got %v", err)
	}
	if !gcp.executeJobCalled {
		t.Error("expected ExecuteJob to be called")
	}
}

func TestApproveAction_Service_Canary0_FallsBackToPercent10(t *testing.T) {
	// given — canary_0 has percent=0; approveService must treat 0 as 10 (first canary step)
	gcp := newMockGCP()
	notifier := &mockNotifier{}
	auth := &mockAuth{authorized: true, expired: false}
	svc := NewRunOpsService(gcp, notifier, auth, &mockStore{})
	req := newServiceReq()
	req.Action = "canary_0"

	// when
	err := svc.ApproveAction(context.Background(), req)

	// then
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if !gcp.shiftTrafficCalled {
		t.Error("expected ShiftTraffic to be called")
	}
	if gcp.shiftTrafficPercent != 10 {
		t.Errorf("expected ShiftTraffic percent=10 for canary_0, got %d", gcp.shiftTrafficPercent)
	}
}

func TestApproveAction_MultiWorkerPool_SecondFails_CompensatesFirst(t *testing.T) {
	// given — first worker pool succeeds, second fails; first must be rolled back to 0%
	gcp := &mockGCP{
		shiftErrOnIdx:      -1,
		workerPoolErrOnIdx: 1,
		updateWorkerPoolErr: errors.New("gcp error"),
	}
	notifier := &mockNotifier{}
	auth := &mockAuth{authorized: true, expired: false}
	svc := NewRunOpsService(gcp, notifier, auth, &mockStore{})
	req := newWorkerPoolReq()
	req.ResourceNames = "pool-a,pool-b"
	req.Targets = "pool-a-v2,pool-b-v2"
	req.Action = "canary_20"

	// when
	err := svc.ApproveAction(context.Background(), req)

	// then — error returned
	if err == nil {
		t.Fatal("expected error when second UpdateWorkerPool fails, got nil")
	}
	// three calls: pool-a@20% (ok), pool-b@20% (fail), pool-a@0% (rollback)
	if len(gcp.workerPoolCalls) != 3 {
		t.Fatalf("expected 3 UpdateWorkerPool calls (2 forward + 1 rollback), got %d: %+v", len(gcp.workerPoolCalls), gcp.workerPoolCalls)
	}
	rollback := gcp.workerPoolCalls[2]
	if rollback.name != "pool-a" || rollback.percent != 0 {
		t.Errorf("compensating rollback: got %+v, want pool-a@0%%", rollback)
	}
}

func TestDenyAction_UnauthorizedUser_StillSucceeds(t *testing.T) {
	// given — DenyAction has no authorization check by design:
	// any user who clicks "Deny" should be able to cancel a deployment.
	gcp := newMockGCP()
	notifier := &mockNotifier{}
	auth := &mockAuth{authorized: false, expired: false} // unauthorized
	svc := NewRunOpsService(gcp, notifier, auth, &mockStore{})
	req := newServiceReq()
	req.ApproverID = "unauthorized-user"

	// when
	err := svc.DenyAction(context.Background(), req)

	// then — must succeed; DenyAction is intentionally auth-free
	if err != nil {
		t.Fatalf("expected nil error for DenyAction without auth, got %v", err)
	}
	if !notifier.replaceMessageCalled {
		t.Error("expected ReplaceMessage to be called")
	}
	if notifier.sendEphemeralCalled {
		t.Error("expected SendEphemeral NOT to be called in DenyAction")
	}
}

func TestApproveAction_MultiService_NextReqPreservesBundle(t *testing.T) {
	// given — multi-service canary_10; the continuation request must carry the same bundle
	gcp := newMockGCP()
	notifier := &mockNotifier{}
	auth := &mockAuth{authorized: true, expired: false}
	svc := NewRunOpsService(gcp, notifier, auth, &mockStore{})
	req := newServiceReq()
	req.ResourceNames = "frontend-service,backend-service"
	req.Targets = "frontend-v2,backend-v2"
	req.Action = "canary_10"

	// when
	err := svc.ApproveAction(context.Background(), req)

	// then
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if notifier.offerContinuationNextReq == nil {
		t.Fatal("expected nextReq to be non-nil")
	}
	if notifier.offerContinuationNextReq.ResourceNames != "frontend-service,backend-service" {
		t.Errorf("nextReq.ResourceNames = %q, want bundle", notifier.offerContinuationNextReq.ResourceNames)
	}
	if notifier.offerContinuationNextReq.Action != "canary_30" {
		t.Errorf("nextReq.Action = %q, want canary_30", notifier.offerContinuationNextReq.Action)
	}
}
