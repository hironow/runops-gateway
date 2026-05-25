package admin_approval_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/hironow/runops-gateway/internal/core/domain"
	"github.com/hironow/runops-gateway/internal/core/port"
	"github.com/hironow/runops-gateway/internal/usecase/admin_approval"
)

// ---- fakes ----

type fakePendingStore struct {
	get        map[string]domain.PendingApproval
	getErr     map[string]error
	transition []transitionCall
	transErr   error
}

type transitionCall struct {
	key       string
	newStatus domain.PendingStatus
	appliedAt *time.Time
}

func newFakePendingStore() *fakePendingStore {
	return &fakePendingStore{get: map[string]domain.PendingApproval{}, getErr: map[string]error{}}
}

func (f *fakePendingStore) CreateIfNotExists(_ context.Context, _ domain.PendingApproval) (domain.PendingApproval, error) {
	return domain.PendingApproval{}, errors.New("not used")
}
func (f *fakePendingStore) Get(_ context.Context, key string) (domain.PendingApproval, error) {
	if err, ok := f.getErr[key]; ok {
		return domain.PendingApproval{}, err
	}
	p, ok := f.get[key]
	if !ok {
		return domain.PendingApproval{}, port.ErrPendingNotFound
	}
	return p, nil
}
func (f *fakePendingStore) Transition(_ context.Context, key string, st domain.PendingStatus, appliedAt *time.Time) error {
	if f.transErr != nil {
		return f.transErr
	}
	f.transition = append(f.transition, transitionCall{key: key, newStatus: st, appliedAt: appliedAt})
	// also reflect into get map so a subsequent Get returns the new status
	if pa, ok := f.get[key]; ok {
		pa.Status = st
		if appliedAt != nil {
			pa.AppliedAt = appliedAt
		}
		f.get[key] = pa
	}
	return nil
}

type fakeRegistry struct {
	added    []domain.Project
	archived []string
	addErr   error
	archErr  error
}

func (r *fakeRegistry) Add(_ context.Context, p domain.Project) error {
	if r.addErr != nil {
		return r.addErr
	}
	r.added = append(r.added, p)
	return nil
}
func (r *fakeRegistry) List(_ context.Context, _ port.ProjectListFilter) ([]domain.Project, error) {
	return nil, errors.New("not used")
}
func (r *fakeRegistry) Get(_ context.Context, _ string) (domain.Project, error) {
	return domain.Project{}, errors.New("not used")
}
func (r *fakeRegistry) Archive(_ context.Context, id string) error {
	if r.archErr != nil {
		return r.archErr
	}
	r.archived = append(r.archived, id)
	return nil
}

// ---- helper ----

func samplePendingAdd(key, requester string) domain.PendingApproval {
	return domain.PendingApproval{
		IdempotencyKey:       key,
		Op:                   domain.PendingOpAdd,
		BodyJSON:             []byte(`{"id":"alpha","github_org":"acme","github_repo":"alpha-repo","workspace_path":"/srv/projects/alpha"}`),
		EffectiveRequesterID: requester,
		RequesterActorType:   "human-operator",
		CreatedAt:            time.Now().UTC().Add(-1 * time.Minute),
		Status:               domain.PendingStatusPendingApproval,
	}
}

func samplePendingArchive(key, requester string) domain.PendingApproval {
	return domain.PendingApproval{
		IdempotencyKey:       key,
		Op:                   domain.PendingOpArchive,
		BodyJSON:             []byte(`{"id":"alpha"}`),
		EffectiveRequesterID: requester,
		RequesterActorType:   "human-operator",
		CreatedAt:            time.Now().UTC().Add(-1 * time.Minute),
		Status:               domain.PendingStatusPendingApproval,
	}
}

// ---- TESTS ----

func TestOnApprovalAck_HappyPath_AppliesAddAndTransitions(t *testing.T) {
	// given
	store := newFakePendingStore()
	store.get["k1"] = samplePendingAdd("k1", "U_ALICE")
	reg := &fakeRegistry{}
	o := admin_approval.NewOrchestrator(store, reg)

	// when - bob approves alice's pending add
	err := o.OnApprovalAck(context.Background(), "k1", "U_BOB", domain.CallerHumanOperator)

	// then
	if err != nil {
		t.Fatalf("OnApprovalAck: %v", err)
	}
	if len(reg.added) != 1 {
		t.Fatalf("registry.Add calls: got %d, want 1", len(reg.added))
	}
	if reg.added[0].ID != "alpha" {
		t.Errorf("project id: got %q, want alpha", reg.added[0].ID)
	}
	if reg.added[0].Status != domain.ProjectStatusActive {
		t.Errorf("project status: got %q, want %q", reg.added[0].Status, domain.ProjectStatusActive)
	}
	if len(store.transition) != 1 {
		t.Fatalf("transition calls: got %d, want 1", len(store.transition))
	}
	tc := store.transition[0]
	if tc.key != "k1" || tc.newStatus != domain.PendingStatusApprovedApplied {
		t.Errorf("transition: got key=%q status=%q", tc.key, tc.newStatus)
	}
	if tc.appliedAt == nil {
		t.Errorf("appliedAt must be non-nil for approved_applied")
	}
}

func TestOnApprovalAck_HappyPath_AppliesArchiveAndTransitions(t *testing.T) {
	store := newFakePendingStore()
	store.get["k2"] = samplePendingArchive("k2", "U_ALICE")
	reg := &fakeRegistry{}
	o := admin_approval.NewOrchestrator(store, reg)

	err := o.OnApprovalAck(context.Background(), "k2", "U_BOB", domain.CallerHumanOperator)
	if err != nil {
		t.Fatalf("OnApprovalAck: %v", err)
	}
	if len(reg.archived) != 1 || reg.archived[0] != "alpha" {
		t.Errorf("registry.Archive calls: got %+v, want [alpha]", reg.archived)
	}
	if len(store.transition) != 1 || store.transition[0].newStatus != domain.PendingStatusApprovedApplied {
		t.Errorf("transition: got %+v", store.transition)
	}
}

func TestOnApprovalAck_SelfApproval_Rejected(t *testing.T) {
	// given - alice tries to approve her own pending
	store := newFakePendingStore()
	store.get["k1"] = samplePendingAdd("k1", "U_ALICE")
	reg := &fakeRegistry{}
	o := admin_approval.NewOrchestrator(store, reg)

	// when
	err := o.OnApprovalAck(context.Background(), "k1", "U_ALICE", domain.CallerHumanOperator)

	// then
	if !errors.Is(err, admin_approval.ErrSelfApproval) {
		t.Errorf("expected ErrSelfApproval, got %v", err)
	}
	if len(reg.added) != 0 {
		t.Errorf("registry must NOT be touched on self-approval; got %d adds", len(reg.added))
	}
	if len(store.transition) != 0 {
		t.Errorf("transition must NOT fire on self-approval; got %d", len(store.transition))
	}
}

func TestOnApprovalAck_NonHumanApprover_Rejected(t *testing.T) {
	store := newFakePendingStore()
	store.get["k1"] = samplePendingAdd("k1", "U_ALICE")
	reg := &fakeRegistry{}
	o := admin_approval.NewOrchestrator(store, reg)

	err := o.OnApprovalAck(context.Background(), "k1", "U_BOB", domain.CallerAIAgent)
	if !errors.Is(err, admin_approval.ErrApproverActorNotHuman) {
		t.Errorf("expected ErrApproverActorNotHuman, got %v", err)
	}
	if len(reg.added) != 0 {
		t.Errorf("registry must NOT be touched; got %d adds", len(reg.added))
	}
}

func TestOnApprovalAck_PendingMissing_ReturnsErrPendingMissing(t *testing.T) {
	store := newFakePendingStore()
	reg := &fakeRegistry{}
	o := admin_approval.NewOrchestrator(store, reg)

	err := o.OnApprovalAck(context.Background(), "unknown", "U_BOB", domain.CallerHumanOperator)
	if !errors.Is(err, admin_approval.ErrPendingMissing) {
		t.Errorf("expected ErrPendingMissing, got %v", err)
	}
}

func TestOnApprovalAck_UnresolvedRequester_FailsClosed(t *testing.T) {
	// given - pending has empty EffectiveRequesterID (= legacy migration value)
	store := newFakePendingStore()
	pa := samplePendingAdd("k1", "")
	store.get["k1"] = pa
	reg := &fakeRegistry{}
	o := admin_approval.NewOrchestrator(store, reg)

	// when
	err := o.OnApprovalAck(context.Background(), "k1", "U_BOB", domain.CallerHumanOperator)

	// then - must refuse to apply
	if !errors.Is(err, admin_approval.ErrUnresolvedRequester) {
		t.Errorf("expected ErrUnresolvedRequester, got %v", err)
	}
	if len(reg.added) != 0 {
		t.Errorf("registry must NOT be touched; got %d adds", len(reg.added))
	}
}

func TestOnApprovalAck_AlreadyApplied_Idempotent(t *testing.T) {
	// given - pending already in approved_applied state
	store := newFakePendingStore()
	applied := time.Now().UTC().Add(-30 * time.Second)
	pa := samplePendingAdd("k1", "U_ALICE")
	pa.Status = domain.PendingStatusApprovedApplied
	pa.AppliedAt = &applied
	store.get["k1"] = pa
	reg := &fakeRegistry{}
	o := admin_approval.NewOrchestrator(store, reg)

	// when - duplicate Slack click
	err := o.OnApprovalAck(context.Background(), "k1", "U_BOB", domain.CallerHumanOperator)

	// then - treat as idempotent success (= return ErrAlreadyTerminal so caller can decide)
	if !errors.Is(err, admin_approval.ErrAlreadyTerminal) {
		t.Errorf("expected ErrAlreadyTerminal, got %v", err)
	}
	if len(reg.added) != 0 {
		t.Errorf("registry must NOT be re-applied; got %d adds", len(reg.added))
	}
}

func TestOnApprovalAck_RegistryError_DoesNotTransition(t *testing.T) {
	// given - registry.Add fails
	store := newFakePendingStore()
	store.get["k1"] = samplePendingAdd("k1", "U_ALICE")
	reg := &fakeRegistry{addErr: errors.New("registry boom")}
	o := admin_approval.NewOrchestrator(store, reg)

	// when
	err := o.OnApprovalAck(context.Background(), "k1", "U_BOB", domain.CallerHumanOperator)

	// then - error surfaced, pending NOT transitioned (= retryable)
	if err == nil {
		t.Fatalf("expected error from registry.Add")
	}
	if len(store.transition) != 0 {
		t.Errorf("transition must NOT fire when apply fails; got %d", len(store.transition))
	}
}

func TestOnApprovalAck_InvalidBodyJSON_ReturnsError(t *testing.T) {
	store := newFakePendingStore()
	pa := samplePendingAdd("k1", "U_ALICE")
	pa.BodyJSON = []byte(`{not json`)
	store.get["k1"] = pa
	reg := &fakeRegistry{}
	o := admin_approval.NewOrchestrator(store, reg)

	err := o.OnApprovalAck(context.Background(), "k1", "U_BOB", domain.CallerHumanOperator)
	if err == nil {
		t.Fatalf("expected decode error")
	}
}

func TestOnApprovalDeny_Transitions_NoApply(t *testing.T) {
	store := newFakePendingStore()
	store.get["k1"] = samplePendingAdd("k1", "U_ALICE")
	reg := &fakeRegistry{}
	o := admin_approval.NewOrchestrator(store, reg)

	err := o.OnApprovalDeny(context.Background(), "k1", "U_BOB", domain.CallerHumanOperator)
	if err != nil {
		t.Fatalf("OnApprovalDeny: %v", err)
	}
	if len(reg.added) != 0 {
		t.Errorf("registry must NOT be touched on deny; got %d adds", len(reg.added))
	}
	if len(store.transition) != 1 || store.transition[0].newStatus != domain.PendingStatusDenied {
		t.Errorf("transition: got %+v, want denied", store.transition)
	}
}

func TestOnApprovalTimeout_TransitionsToTimeout(t *testing.T) {
	store := newFakePendingStore()
	store.get["k1"] = samplePendingAdd("k1", "U_ALICE")
	reg := &fakeRegistry{}
	o := admin_approval.NewOrchestrator(store, reg)

	err := o.OnApprovalTimeout(context.Background(), "k1")
	if err != nil {
		t.Fatalf("OnApprovalTimeout: %v", err)
	}
	if len(reg.added) != 0 {
		t.Errorf("registry must NOT be touched on timeout; got %d adds", len(reg.added))
	}
	if len(store.transition) != 1 || store.transition[0].newStatus != domain.PendingStatusTimeout {
		t.Errorf("transition: got %+v, want timeout", store.transition)
	}
}

func TestOrchestrator_NilDependenciesPanic(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("expected panic on nil deps")
		}
	}()
	_ = admin_approval.NewOrchestrator(nil, nil)
	_ = json.RawMessage{} // touch import to avoid unused-import
}
