package usecase

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hironow/runops-gateway/internal/adapter/output/state"
	"github.com/hironow/runops-gateway/internal/core/domain"
	"github.com/hironow/runops-gateway/internal/core/port"
)

// --- mock implementations ---

type shiftCall struct {
	project  string
	location string
	name     string
	target   string
	percent  int32
}

type workerPoolCall struct {
	project  string
	location string
	name     string
	target   string
	percent  int32
}

type executeJobCall struct {
	project  string
	location string
	jobName  string
	args     []string
}

type triggerBackupCall struct {
	project      string
	instanceName string
}

type mockGCP struct {
	shiftCalls             []shiftCall // all recorded ShiftTraffic calls in order
	shiftTrafficCalled     bool
	shiftTrafficPercent    int32
	shiftErrOnIdx          int           // if >= 0, return shiftTrafficErr on this call index
	shiftErrOnIndices      map[int]error // multiple indices with specific errors (takes priority over shiftErrOnIdx)
	shiftTrafficErr        error
	executeJobCalled       bool
	executeJobErr          error
	executeJobCalls        []executeJobCall
	triggerBackupCalled    bool
	triggerBackupErr       error
	triggerBackupCalls     []triggerBackupCall
	updateWorkerPoolCalled bool
	updateWorkerPoolErr    error
	workerPoolCalls        []workerPoolCall // all recorded UpdateWorkerPool calls in order
	workerPoolErrOnIdx     int              // if >= 0, return updateWorkerPoolErr on this call index
}

func newMockGCP() *mockGCP { return &mockGCP{shiftErrOnIdx: -1, workerPoolErrOnIdx: -1} }

func (m *mockGCP) ShiftTraffic(_ context.Context, project, location, name, target string, percent int32) error {
	idx := len(m.shiftCalls)
	m.shiftCalls = append(m.shiftCalls, shiftCall{project, location, name, target, percent})
	m.shiftTrafficCalled = true
	m.shiftTrafficPercent = percent
	if e, ok := m.shiftErrOnIndices[idx]; ok {
		return e
	}
	if m.shiftErrOnIdx >= 0 && idx == m.shiftErrOnIdx {
		return m.shiftTrafficErr
	}
	return nil
}

func (m *mockGCP) ExecuteJob(_ context.Context, project, location, jobName string, args []string) error {
	m.executeJobCalled = true
	m.executeJobCalls = append(m.executeJobCalls, executeJobCall{project, location, jobName, args})
	return m.executeJobErr
}

func (m *mockGCP) TriggerBackup(_ context.Context, project, instanceName string) error {
	m.triggerBackupCalled = true
	m.triggerBackupCalls = append(m.triggerBackupCalls, triggerBackupCall{project, instanceName})
	return m.triggerBackupErr
}

func (m *mockGCP) UpdateWorkerPool(_ context.Context, project, location, name, target string, percent int32) error {
	idx := len(m.workerPoolCalls)
	m.workerPoolCalls = append(m.workerPoolCalls, workerPoolCall{project, location, name, target, percent})
	m.updateWorkerPoolCalled = true
	if m.workerPoolErrOnIdx >= 0 && idx == m.workerPoolErrOnIdx {
		return m.updateWorkerPoolErr
	}
	return nil
}

type mockNotifier struct {
	updateMessageCalled      bool
	updateMessageText        string
	updateMessageTexts       []string
	updateMessageErr         error
	replaceMessageCalled     bool
	replaceMessageText       string
	replaceErr               error
	sendEphemeralCalled      bool
	sendEphemeralText        string
	offerContinuationCalled  bool
	offerContinuationSummary string
	offerContinuationNextReq *domain.ApprovalRequest
	offerContinuationStopReq *domain.ApprovalRequest
	offerContinuationErr     error
	rebuildInitialCalled     bool
	rebuildInitialErrMsg     string
	rebuildInitialJobReq     *domain.ApprovalRequest
	rebuildInitialSvcReq     *domain.ApprovalRequest
	rebuildInitialDenyReq    *domain.ApprovalRequest
	rebuildInitialErr        error
}

func (m *mockNotifier) UpdateMessage(_ context.Context, _ port.NotifyTarget, text string) error {
	m.updateMessageCalled = true
	m.updateMessageText = text
	m.updateMessageTexts = append(m.updateMessageTexts, text)
	return m.updateMessageErr
}

func (m *mockNotifier) ReplaceMessage(_ context.Context, _ port.NotifyTarget, text string) error {
	m.replaceMessageCalled = true
	m.replaceMessageText = text
	return m.replaceErr
}

func (m *mockNotifier) SendEphemeral(_ context.Context, _ port.NotifyTarget, _ string, text string) error {
	m.sendEphemeralCalled = true
	m.sendEphemeralText = text
	return nil
}

func (m *mockNotifier) OfferContinuation(_ context.Context, _ port.NotifyTarget, summary string, nextReq *domain.ApprovalRequest, stopReq *domain.ApprovalRequest) error {
	m.offerContinuationCalled = true
	m.offerContinuationSummary = summary
	m.offerContinuationNextReq = nextReq
	m.offerContinuationStopReq = stopReq
	return m.offerContinuationErr
}

func (m *mockNotifier) RebuildInitialApproval(_ context.Context, _ port.NotifyTarget, errMsg string, jobReq, svcReq, denyReq *domain.ApprovalRequest) error {
	m.rebuildInitialCalled = true
	m.rebuildInitialErrMsg = errMsg
	m.rebuildInitialJobReq = jobReq
	m.rebuildInitialSvcReq = svcReq
	m.rebuildInitialDenyReq = denyReq
	return m.rebuildInitialErr
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

var testTarget = port.NotifyTarget{CallbackURL: "https://hooks.slack.com/xxx", Mode: port.ModeSlack}

func newServiceReq() domain.ApprovalRequest {
	return domain.ApprovalRequest{
		Project:       "test-project",
		Location:      "asia-northeast1",
		ResourceType:  domain.ResourceTypeService,
		ResourceNames: "frontend-service",
		Targets:       "frontend-service-v2",
		Action:        "canary_10",
		ApproverID:    "U123",
		IssuedAt:      time.Now().Unix(),
	}
}

func newJobReq() domain.ApprovalRequest {
	return domain.ApprovalRequest{
		Project:       "test-project",
		Location:      "asia-northeast1",
		ResourceType:  domain.ResourceTypeJob,
		ResourceNames: "migration-job",
		Targets:       "",
		Action:        "migrate_apply",
		ApproverID:    "U123",
		IssuedAt:      time.Now().Unix(),
	}
}

func newWorkerPoolReq() domain.ApprovalRequest {
	return domain.ApprovalRequest{
		Project:       "test-project",
		Location:      "asia-northeast1",
		ResourceType:  domain.ResourceTypeWorkerPool,
		ResourceNames: "batch-pool",
		Targets:       "batch-pool-v2",
		Action:        "canary_20",
		ApproverID:    "U123",
		IssuedAt:      time.Now().Unix(),
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
	err := svc.ApproveAction(context.Background(), req, testTarget)

	// then
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if !gcp.shiftTrafficCalled {
		t.Error("expected ShiftTraffic to be called")
	}
	if len(gcp.shiftCalls) == 0 {
		t.Fatal("expected at least one ShiftTraffic call")
	}
	if gcp.shiftCalls[0].project != "test-project" {
		t.Errorf("expected project=test-project, got %q", gcp.shiftCalls[0].project)
	}
	if gcp.shiftCalls[0].location != "asia-northeast1" {
		t.Errorf("expected location=asia-northeast1, got %q", gcp.shiftCalls[0].location)
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
	err := svc.ApproveAction(context.Background(), req, testTarget)

	// then
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if !gcp.triggerBackupCalled {
		t.Error("expected TriggerBackup to be called")
	}
	if len(gcp.triggerBackupCalls) == 0 {
		t.Fatal("expected at least one TriggerBackup call")
	}
	if gcp.triggerBackupCalls[0].project != "test-project" {
		t.Errorf("expected TriggerBackup project=test-project, got %q", gcp.triggerBackupCalls[0].project)
	}
	if !gcp.executeJobCalled {
		t.Error("expected ExecuteJob to be called")
	}
	if len(gcp.executeJobCalls) == 0 {
		t.Fatal("expected at least one ExecuteJob call")
	}
	if gcp.executeJobCalls[0].project != "test-project" {
		t.Errorf("expected ExecuteJob project=test-project, got %q", gcp.executeJobCalls[0].project)
	}
	if gcp.executeJobCalls[0].location != "asia-northeast1" {
		t.Errorf("expected ExecuteJob location=asia-northeast1, got %q", gcp.executeJobCalls[0].location)
	}
	if !notifier.replaceMessageCalled {
		t.Error("expected ReplaceMessage to be called")
	}
	if notifier.replaceMessageText == "" {
		t.Fatal("expected non-empty ReplaceMessage text")
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
	err := svc.ApproveAction(context.Background(), req, testTarget)

	// then
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if !gcp.updateWorkerPoolCalled {
		t.Error("expected UpdateWorkerPool to be called")
	}
	if len(gcp.workerPoolCalls) == 0 {
		t.Fatal("expected at least one UpdateWorkerPool call")
	}
	if gcp.workerPoolCalls[0].project != "test-project" {
		t.Errorf("expected project=test-project, got %q", gcp.workerPoolCalls[0].project)
	}
	if gcp.workerPoolCalls[0].location != "asia-northeast1" {
		t.Errorf("expected location=asia-northeast1, got %q", gcp.workerPoolCalls[0].location)
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
	err := svc.ApproveAction(context.Background(), req, testTarget)

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
	err := svc.ApproveAction(context.Background(), req, testTarget)

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
	err := svc.ApproveAction(context.Background(), req, testTarget)

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
	err := svc.ApproveAction(context.Background(), req, testTarget)

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
	err := svc.ApproveAction(context.Background(), req, testTarget)

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
	err := svc.DenyAction(context.Background(), req, testTarget)

	// then
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if !notifier.replaceMessageCalled {
		t.Error("expected ReplaceMessage to be called")
	}
	if notifier.replaceMessageText == "" {
		t.Fatal("expected non-empty ReplaceMessage text")
	}
}

func TestApproveAction_CLIMode(t *testing.T) {
	// given
	gcp := newMockGCP()
	notifier := &mockNotifier{}
	auth := &mockAuth{authorized: true, expired: false}
	svc := NewRunOpsService(gcp, notifier, auth, &mockStore{})
	req := newServiceReq()
	stdoutTarget := port.NotifyTarget{Mode: port.ModeStdout}

	// when — verify no panic and mode logic is exercised
	err := svc.ApproveAction(context.Background(), req, stdoutTarget)

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
	err := svc.ApproveAction(context.Background(), req, testTarget)

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
	err := svc.ApproveAction(context.Background(), req, testTarget)

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
	err := svc.ApproveAction(context.Background(), req, testTarget)

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
	err := svc.ApproveAction(context.Background(), req, testTarget)

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
	err := svc.ApproveAction(context.Background(), req, testTarget)

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
	err := svc.ApproveAction(context.Background(), req, testTarget)

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
	if notifier.offerContinuationNextReq.Project != "test-project" {
		t.Errorf("expected nextReq.Project=test-project, got %q", notifier.offerContinuationNextReq.Project)
	}
	if notifier.offerContinuationNextReq.Location != "asia-northeast1" {
		t.Errorf("expected nextReq.Location=asia-northeast1, got %q", notifier.offerContinuationNextReq.Location)
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
	err := svc.ApproveAction(context.Background(), req, testTarget)

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
	err := svc.ApproveAction(context.Background(), req, testTarget)

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
	err := svc.ApproveAction(context.Background(), req, testTarget)

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
	err := svc.DenyAction(context.Background(), req, testTarget)

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
	err := svc.ApproveAction(context.Background(), req, testTarget)

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
	err2 := svc.ApproveAction(context.Background(), req, testTarget)

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
	err := svc.ApproveAction(context.Background(), req, testTarget)

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
	err := svc.ApproveAction(context.Background(), req, testTarget)

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
	err := svc.ApproveAction(context.Background(), req, testTarget)

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
	err := svc.ApproveAction(context.Background(), req, testTarget)

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
	err := svc.ApproveAction(context.Background(), req, testTarget)

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
	err := svc.ApproveAction(context.Background(), req, testTarget)

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
		shiftErrOnIdx:       -1,
		workerPoolErrOnIdx:  1,
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
	err := svc.ApproveAction(context.Background(), req, testTarget)

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
	err := svc.DenyAction(context.Background(), req, testTarget)

	// then — must succeed; DenyAction is intentionally auth-free
	if err != nil {
		t.Fatalf("expected nil error for DenyAction without auth, got %v", err)
	}
	if !notifier.replaceMessageCalled {
		t.Error("expected ReplaceMessage to be called")
	}
	if notifier.replaceMessageText == "" {
		t.Fatal("expected non-empty ReplaceMessage text")
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
	err := svc.ApproveAction(context.Background(), req, testTarget)

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

// --- OfferContinuation failure tests ---

func TestApproveAction_Service_OfferContinuationFails_StillReturnsNil(t *testing.T) {
	// given — OfferContinuation fails (e.g. Slack returns 404)
	// ApproveAction should still return nil because the GCP operation itself succeeded.
	gcp := newMockGCP()
	notifier := &mockNotifier{offerContinuationErr: errors.New("slack notifier: unexpected status 404")}
	auth := &mockAuth{authorized: true, expired: false}
	svc := NewRunOpsService(gcp, notifier, auth, &mockStore{})
	req := newServiceReq()

	// when
	err := svc.ApproveAction(context.Background(), req, testTarget)

	// then — no error returned (GCP succeeded, notification is best-effort)
	if err != nil {
		t.Fatalf("expected nil error (GCP succeeded), got %v", err)
	}
	if !gcp.shiftTrafficCalled {
		t.Error("expected ShiftTraffic to be called")
	}
	if !notifier.offerContinuationCalled {
		t.Error("expected OfferContinuation to be called (even though it fails)")
	}
}

func TestApproveAction_Service_OfferContinuation_IncludesStopReq(t *testing.T) {
	// given — canary_10 should offer both next (canary_30) and stop (rollback)
	gcp := newMockGCP()
	notifier := &mockNotifier{}
	auth := &mockAuth{authorized: true, expired: false}
	svc := NewRunOpsService(gcp, notifier, auth, &mockStore{})
	req := newServiceReq()
	req.Action = "canary_10"

	// when
	err := svc.ApproveAction(context.Background(), req, testTarget)

	// then
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if notifier.offerContinuationNextReq == nil {
		t.Fatal("expected nextReq to be non-nil for canary_10")
	}
	if notifier.offerContinuationNextReq.Action != "canary_30" {
		t.Errorf("nextReq.Action = %q, want canary_30", notifier.offerContinuationNextReq.Action)
	}
	if notifier.offerContinuationStopReq == nil {
		t.Fatal("expected stopReq to be non-nil for canary_10")
	}
	if notifier.offerContinuationStopReq.Action != "rollback" {
		t.Errorf("stopReq.Action = %q, want rollback", notifier.offerContinuationStopReq.Action)
	}
}

func TestApproveAction_Service_Canary100_NoNextStep(t *testing.T) {
	// given — canary_100 is the final step, no next/stop buttons
	gcp := newMockGCP()
	notifier := &mockNotifier{}
	auth := &mockAuth{authorized: true, expired: false}
	svc := NewRunOpsService(gcp, notifier, auth, &mockStore{})
	req := newServiceReq()
	req.Action = "canary_100"

	// when
	err := svc.ApproveAction(context.Background(), req, testTarget)

	// then
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if notifier.offerContinuationNextReq != nil {
		t.Error("expected nextReq to be nil for canary_100 (final step)")
	}
	if notifier.offerContinuationStopReq != nil {
		t.Error("expected stopReq to be nil for canary_100 (final step)")
	}
}

func TestApproveAction_Service_ShiftFails_OfferRetryButton(t *testing.T) {
	// given — ShiftTraffic fails, should call OfferContinuation with error message + retry button
	gcp := newMockGCP()
	gcp.shiftErrOnIdx = 0
	gcp.shiftTrafficErr = errors.New("permission denied")
	notifier := &mockNotifier{}
	auth := &mockAuth{authorized: true, expired: false}
	svc := NewRunOpsService(gcp, notifier, auth, &mockStore{})
	req := newServiceReq()

	// when
	err := svc.ApproveAction(context.Background(), req, testTarget)

	// then — returns error
	if err == nil {
		t.Fatal("expected error from ShiftTraffic failure")
	}
	// OfferContinuation is called (via offerRetry) with retry button
	if !notifier.offerContinuationCalled {
		t.Error("expected OfferContinuation to be called for retry button")
	}
	// The summary should contain the error message
	if notifier.offerContinuationSummary == "" {
		t.Error("expected non-empty error summary in OfferContinuation")
	}
	// nextReq (retry button) should preserve the original action
	if notifier.offerContinuationNextReq == nil {
		t.Fatal("expected retry button (nextReq) to be non-nil")
	}
	if notifier.offerContinuationNextReq.Action != req.Action {
		t.Errorf("retry button action = %q, want %q", notifier.offerContinuationNextReq.Action, req.Action)
	}
	if notifier.offerContinuationNextReq.Project != req.Project {
		t.Errorf("retry button project = %q, want %q", notifier.offerContinuationNextReq.Project, req.Project)
	}
}

func TestApproveAction_Job_BackupFails_RebuildsInitialApproval(t *testing.T) {
	// given — TriggerBackup fails. The user pressed "1. DB Migration → Canary"
	// but backup is non-recoverable (e.g. 403). Instead of offering a retry
	// (which would just re-trigger the same backup), the message must rebuild
	// the original 3-button prompt so the operator can pick "Canary skip
	// migration" or "Deny" instead.
	gcp := newMockGCP()
	gcp.triggerBackupErr = errors.New("backup failed: 403")
	notifier := &mockNotifier{}
	auth := &mockAuth{authorized: true, expired: false}
	svc := NewRunOpsService(gcp, notifier, auth, &mockStore{})
	req := newJobReq()
	req.NextServiceNames = "frontend-service"
	req.NextRevisions = "frontend-service-v2"

	// when
	err := svc.ApproveAction(context.Background(), req, testTarget)

	// then
	if err == nil {
		t.Fatal("expected error from TriggerBackup failure")
	}
	if notifier.offerContinuationCalled {
		t.Error("expected retry button (OfferContinuation) NOT to be used after non-recoverable backup error")
	}
	if !notifier.rebuildInitialCalled {
		t.Fatal("expected RebuildInitialApproval to be called so user returns to initial 3-button state")
	}
	if notifier.rebuildInitialErrMsg == "" {
		t.Error("expected non-empty error message in rebuild")
	}
	if notifier.rebuildInitialJobReq == nil {
		t.Fatal("expected job button to be rebuilt (button 1)")
	}
	if notifier.rebuildInitialJobReq.Action != "migrate_apply" {
		t.Errorf("job button action = %q, want migrate_apply", notifier.rebuildInitialJobReq.Action)
	}
	if notifier.rebuildInitialSvcReq == nil {
		t.Fatal("expected service button to be rebuilt (button 2 — canary skip migration)")
	}
	if notifier.rebuildInitialSvcReq.Action != "canary_10" {
		t.Errorf("service button action = %q, want canary_10", notifier.rebuildInitialSvcReq.Action)
	}
	if notifier.rebuildInitialSvcReq.ResourceNames != "frontend-service" {
		t.Errorf("service button ResourceNames = %q, want frontend-service", notifier.rebuildInitialSvcReq.ResourceNames)
	}
	if notifier.rebuildInitialSvcReq.Targets != "frontend-service-v2" {
		t.Errorf("service button Targets = %q, want frontend-service-v2", notifier.rebuildInitialSvcReq.Targets)
	}
	if notifier.rebuildInitialDenyReq == nil {
		t.Fatal("expected deny button to be rebuilt (button 3)")
	}
	if notifier.rebuildInitialDenyReq.Action != "deny" {
		t.Errorf("deny button action = %q, want deny", notifier.rebuildInitialDenyReq.Action)
	}
}

func TestApproveAction_Job_EmptyResourceNames_RejectsEarly(t *testing.T) {
	// given — Misconfigured deploy: cloudbuild's _MIGRATION_JOB_NAME is empty,
	// but somehow a migrate_apply request still arrived. We must not submit a
	// Cloud SQL backup against an empty instance name.
	gcp := newMockGCP()
	notifier := &mockNotifier{}
	auth := &mockAuth{authorized: true, expired: false}
	svc := NewRunOpsService(gcp, notifier, auth, &mockStore{})
	req := newJobReq()
	req.ResourceNames = ""

	// when
	err := svc.ApproveAction(context.Background(), req, testTarget)

	// then
	if err == nil {
		t.Fatal("expected error for empty ResourceNames in migrate_apply request")
	}
	if gcp.triggerBackupCalled {
		t.Error("TriggerBackup must NOT be called when ResourceNames is empty (would 403 against empty instance)")
	}
	if gcp.executeJobCalled {
		t.Error("ExecuteJob must NOT be called when ResourceNames is empty")
	}
	if !notifier.rebuildInitialCalled {
		t.Error("expected RebuildInitialApproval to surface the misconfiguration to the operator")
	}
}

func TestApproveAction_Job_MigrationFails_RebuildsInitialApproval(t *testing.T) {
	// given — Backup succeeds, ExecuteJob fails. Same UX: rebuild the prompt
	// so the operator can choose a different path (skip migration, deny, etc.).
	gcp := newMockGCP()
	gcp.executeJobErr = errors.New("migration script crashed")
	notifier := &mockNotifier{}
	auth := &mockAuth{authorized: true, expired: false}
	svc := NewRunOpsService(gcp, notifier, auth, &mockStore{})
	req := newJobReq()
	req.NextServiceNames = "frontend-service"
	req.NextRevisions = "frontend-service-v2"

	// when
	err := svc.ApproveAction(context.Background(), req, testTarget)

	// then
	if err == nil {
		t.Fatal("expected error from ExecuteJob failure")
	}
	if !notifier.rebuildInitialCalled {
		t.Fatal("expected RebuildInitialApproval after migration failure")
	}
	if notifier.offerContinuationCalled {
		t.Error("expected retry button NOT to be used after migration failure")
	}
}

func TestApproveAction_WorkerPool_OfferContinuationFails_StillReturnsNil(t *testing.T) {
	// given — GCP succeeds but OfferContinuation fails
	gcp := newMockGCP()
	notifier := &mockNotifier{offerContinuationErr: errors.New("slack 404")}
	auth := &mockAuth{authorized: true, expired: false}
	svc := NewRunOpsService(gcp, notifier, auth, state.NewMemoryStore())
	req := newWorkerPoolReq()

	// when
	err := svc.ApproveAction(context.Background(), req, testTarget)

	// then — no error (GCP succeeded)
	if err != nil {
		t.Fatalf("expected nil error (GCP succeeded), got %v", err)
	}
	if !gcp.updateWorkerPoolCalled {
		t.Error("expected UpdateWorkerPool to be called")
	}
}

// --- Compensating rollback message accuracy tests ---

func TestApproveAction_MultiService_PartialFailure_RollbackSucceeds_MessageSaysRolledBack(t *testing.T) {
	// given — 2 services, second fails, rollback of first succeeds
	gcp := newMockGCP()
	gcp.shiftErrOnIdx = 1 // fail on second service
	gcp.shiftTrafficErr = errors.New("permission denied on svc-B")
	notifier := &mockNotifier{}
	auth := &mockAuth{authorized: true, expired: false}
	svc := NewRunOpsService(gcp, notifier, auth, &mockStore{})
	req := newServiceReq()
	req.ResourceNames = "svc-A,svc-B"
	req.Targets = "svc-A-v2,svc-B-v2"

	// when
	err := svc.ApproveAction(context.Background(), req, testTarget)

	// then
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(notifier.offerContinuationSummary, "ロールバック済み") {
		t.Errorf("expected 'ロールバック済み' in summary, got: %s", notifier.offerContinuationSummary)
	}
	// Compensating rollback called svc-A with 0%
	if len(gcp.shiftCalls) < 2 {
		t.Fatalf("expected at least 2 ShiftTraffic calls (1 forward + 1 rollback), got %d", len(gcp.shiftCalls))
	}
	lastCall := gcp.shiftCalls[len(gcp.shiftCalls)-1]
	if lastCall.percent != 0 {
		t.Errorf("rollback call percent = %d, want 0", lastCall.percent)
	}
}

func TestApproveAction_MultiService_RollbackAlsoFails_MessageWarnsPartialFailure(t *testing.T) {
	// given — 3 services: svc-A(ok), svc-B(ok), svc-C(fail)
	// Compensating: rollback-svc-A(ok), rollback-svc-B(FAIL)
	// Call indices: 0=svc-A, 1=svc-B, 2=svc-C(fail), 3=rollback-svc-A, 4=rollback-svc-B
	gcp := newMockGCP()
	gcp.shiftErrOnIndices = map[int]error{
		2: errors.New("svc-C error"),
		4: errors.New("rollback svc-B failed"),
	}

	notifier := &mockNotifier{}
	auth := &mockAuth{authorized: true, expired: false}
	svcObj := NewRunOpsService(gcp, notifier, auth, &mockStore{})
	req := newServiceReq()
	req.ResourceNames = "svc-A,svc-B,svc-C"
	req.Targets = "svc-A-v2,svc-B-v2,svc-C-v2"

	// when
	err := svcObj.ApproveAction(context.Background(), req, testTarget)

	// then
	if err == nil {
		t.Fatal("expected error")
	}
	// Must warn about partial rollback failure
	if !strings.Contains(notifier.offerContinuationSummary, "一部ロールバック失敗") {
		t.Errorf("expected '一部ロールバック失敗' in summary, got: %s", notifier.offerContinuationSummary)
	}
	// Must include the failed resource name
	if !strings.Contains(notifier.offerContinuationSummary, "svc-B") {
		t.Errorf("expected failed resource 'svc-B' in summary, got: %s", notifier.offerContinuationSummary)
	}
}

func TestApproveAction_MultiService_RollbackSucceeds_MessageSaysRolledBack(t *testing.T) {
	// given — 3 services: svc-A(ok), svc-B(ok), svc-C(fail)
	// Compensating: rollback-svc-A(ok), rollback-svc-B(ok)
	gcp := newMockGCP()
	gcp.shiftErrOnIndices = map[int]error{
		2: errors.New("svc-C error"),
	}

	notifier := &mockNotifier{}
	auth := &mockAuth{authorized: true, expired: false}
	svcObj := NewRunOpsService(gcp, notifier, auth, &mockStore{})
	req := newServiceReq()
	req.ResourceNames = "svc-A,svc-B,svc-C"
	req.Targets = "svc-A-v2,svc-B-v2,svc-C-v2"

	// when
	err := svcObj.ApproveAction(context.Background(), req, testTarget)

	// then
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(notifier.offerContinuationSummary, "ロールバック済み") {
		t.Errorf("expected 'ロールバック済み' in summary, got: %s", notifier.offerContinuationSummary)
	}
	// Must NOT contain the partial failure warning
	if strings.Contains(notifier.offerContinuationSummary, "一部ロールバック失敗") {
		t.Error("should not warn about partial failure when all rollbacks succeed")
	}
}

func TestApproveAction_SingleService_Fails_NoRollbackNeeded(t *testing.T) {
	// given — single service fails, no compensating rollback needed
	gcp := newMockGCP()
	gcp.shiftErrOnIdx = 0
	gcp.shiftTrafficErr = errors.New("fail")
	notifier := &mockNotifier{}
	auth := &mockAuth{authorized: true, expired: false}
	svc := NewRunOpsService(gcp, notifier, auth, &mockStore{})
	req := newServiceReq()

	// when
	err := svc.ApproveAction(context.Background(), req, testTarget)

	// then
	if err == nil {
		t.Fatal("expected error")
	}
	// No resources were successfully shifted, so message should say "ロールバック不要"
	if !strings.Contains(notifier.offerContinuationSummary, "ロールバック不要") {
		t.Errorf("expected 'ロールバック不要' for single-service failure, got: %s", notifier.offerContinuationSummary)
	}
}

// --- Notification fallback tests ---

func TestApproveAction_Service_OfferContinuationFails_FallbackNotifySent(t *testing.T) {
	// given — GCP succeeds but OfferContinuation fails (e.g. invalid_blocks)
	gcp := newMockGCP()
	notifier := &mockNotifier{offerContinuationErr: errors.New("slack validate: duplicate action_id")}
	auth := &mockAuth{authorized: true, expired: false}
	svc := NewRunOpsService(gcp, notifier, auth, &mockStore{})
	req := newServiceReq()

	// when
	_ = svc.ApproveAction(context.Background(), req, testTarget)

	// then — a fallback UpdateMessage must be sent with error notice
	lastMsg := notifier.updateMessageText
	if !strings.Contains(lastMsg, "ログを確認") {
		t.Errorf("expected fallback message with 'ログを確認', got: %q", lastMsg)
	}
}

func TestApproveAction_Job_OfferContinuationFails_FallbackNotifySent(t *testing.T) {
	// given — job succeeds but OfferContinuation fails
	gcp := newMockGCP()
	notifier := &mockNotifier{offerContinuationErr: errors.New("slack 404")}
	auth := &mockAuth{authorized: true, expired: false}
	svc := NewRunOpsService(gcp, notifier, auth, &mockStore{})
	req := newJobReq()
	req.NextServiceNames = "svc-A"
	req.NextRevisions = "svc-A-v2"
	req.NextAction = "canary_10"

	// when
	_ = svc.ApproveAction(context.Background(), req, testTarget)

	// then
	lastMsg := notifier.updateMessageText
	if !strings.Contains(lastMsg, "ログを確認") {
		t.Errorf("expected fallback message with '���グを確認', got: %q", lastMsg)
	}
}

func TestOfferRetry_Fails_FallbackNotifySent(t *testing.T) {
	// given — ShiftTraffic fails AND offerRetry (OfferContinuation) also fails
	gcp := newMockGCP()
	gcp.shiftErrOnIdx = 0
	gcp.shiftTrafficErr = errors.New("gcp error")
	notifier := &mockNotifier{offerContinuationErr: errors.New("slack 404")}
	auth := &mockAuth{authorized: true, expired: false}
	svc := NewRunOpsService(gcp, notifier, auth, &mockStore{})
	req := newServiceReq()

	// when
	_ = svc.ApproveAction(context.Background(), req, testTarget)

	// then — fallback UpdateMessage should contain error notice
	lastMsg := notifier.updateMessageText
	if !strings.Contains(lastMsg, "ログを確認") {
		t.Errorf("expected fallback message with 'ログを確認', got: %q", lastMsg)
	}
}
