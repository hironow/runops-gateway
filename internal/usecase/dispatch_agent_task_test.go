package usecase_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/hironow/runops-gateway/internal/core/domain"
	"github.com/hironow/runops-gateway/internal/core/port"
	"github.com/hironow/runops-gateway/internal/usecase"
)

// --- test doubles ---

type recordedDispatch struct {
	mu   sync.Mutex
	reqs []domain.DispatchRequest
	err  error
}

func (r *recordedDispatch) Dispatch(_ context.Context, req domain.DispatchRequest) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reqs = append(r.reqs, req)
	return r.err
}

type recordedNotifier struct {
	mu          sync.Mutex
	updates     []string
	ephemerals  []ephemeralCall
	replaceTxts []string
}

type ephemeralCall struct {
	UserID string
	Text   string
}

func (n *recordedNotifier) UpdateMessage(_ context.Context, _ port.NotifyTarget, text string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.updates = append(n.updates, text)
	return nil
}

func (n *recordedNotifier) ReplaceMessage(_ context.Context, _ port.NotifyTarget, text string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.replaceTxts = append(n.replaceTxts, text)
	return nil
}

func (n *recordedNotifier) SendEphemeral(_ context.Context, _ port.NotifyTarget, userID, text string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.ephemerals = append(n.ephemerals, ephemeralCall{UserID: userID, Text: text})
	return nil
}

func (n *recordedNotifier) OfferContinuation(_ context.Context, _ port.NotifyTarget, _ string, _, _ *domain.ApprovalRequest) error {
	return nil
}

func (n *recordedNotifier) RebuildInitialApproval(_ context.Context, _ port.NotifyTarget, _ string, _, _, _ *domain.ApprovalRequest) error {
	return nil
}

type allowAuth struct{ allowed map[string]bool }

func (a *allowAuth) IsAuthorized(id string) bool { return a.allowed[id] }
func (a *allowAuth) IsExpired(_ int64) bool      { return false }

type memStore struct {
	mu   sync.Mutex
	keys map[string]bool
}

func newMemStore() *memStore { return &memStore{keys: map[string]bool{}} }

func (m *memStore) TryLock(key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.keys[key] {
		return false
	}
	m.keys[key] = true
	return true
}

func (m *memStore) Release(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.keys, key)
}

// --- tests ---

func TestDispatchAgentTask_HappyPath_DispatchesAndReplies(t *testing.T) {
	// given
	disp := &recordedDispatch{}
	notif := &recordedNotifier{}
	auth := &allowAuth{allowed: map[string]bool{"U0123": true}}
	store := newMemStore()
	svc := usecase.NewDispatchService(disp, notif, auth, store)
	req := domain.DispatchRequest{
		Role:           domain.AgentRolePaintress,
		Text:           "fix M-42",
		RequesterID:    "U0123",
		IdempotencyKey: "k-001",
		IssuedAt:       1700000000,
	}
	target := port.NotifyTarget{CallbackURL: "https://hooks.slack.com/x", Mode: port.ModeSlack}

	// when
	err := svc.DispatchAgentTask(context.Background(), req, target)

	// then
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(disp.reqs) != 1 {
		t.Fatalf("expected 1 dispatch, got %d", len(disp.reqs))
	}
	if disp.reqs[0].Role != domain.AgentRolePaintress {
		t.Errorf("dispatched role=%q", disp.reqs[0].Role)
	}
	if len(notif.updates) == 0 {
		t.Error("expected at least one UpdateMessage for thread reply")
	}
}

func TestDispatchAgentTask_RejectsUnauthorizedUser(t *testing.T) {
	disp := &recordedDispatch{}
	notif := &recordedNotifier{}
	auth := &allowAuth{allowed: map[string]bool{}} // empty allowlist
	store := newMemStore()
	svc := usecase.NewDispatchService(disp, notif, auth, store)
	req := domain.DispatchRequest{
		Role:        domain.AgentRolePaintress,
		Text:        "fix",
		RequesterID: "U0xxx",
	}
	target := port.NotifyTarget{CallbackURL: "https://hooks.slack.com/x", Mode: port.ModeSlack}

	err := svc.DispatchAgentTask(context.Background(), req, target)
	if err == nil {
		t.Fatal("expected unauthorized error, got nil")
	}
	if len(disp.reqs) != 0 {
		t.Errorf("dispatch must not run for unauthorized user; got %d", len(disp.reqs))
	}
	if len(notif.ephemerals) != 1 {
		t.Errorf("expected 1 ephemeral notification, got %d", len(notif.ephemerals))
	}
	if notif.ephemerals[0].UserID != "U0xxx" {
		t.Errorf("ephemeral target user mismatch: %q", notif.ephemerals[0].UserID)
	}
}

func TestDispatchAgentTask_DedupsByOperationKey(t *testing.T) {
	disp := &recordedDispatch{}
	notif := &recordedNotifier{}
	auth := &allowAuth{allowed: map[string]bool{"U0123": true}}
	store := newMemStore()
	svc := usecase.NewDispatchService(disp, notif, auth, store)
	req := domain.DispatchRequest{
		Role:           domain.AgentRolePaintress,
		Text:           "fix",
		RequesterID:    "U0123",
		IdempotencyKey: "k-dup",
		IssuedAt:       1700000000,
	}
	target := port.NotifyTarget{CallbackURL: "https://hooks.slack.com/x", Mode: port.ModeSlack}

	// pre-claim the key so the second call sees it locked
	store.TryLock(req.OperationKey())

	err := svc.DispatchAgentTask(context.Background(), req, target)
	if err == nil {
		t.Fatal("expected dedup error, got nil")
	}
	if len(disp.reqs) != 0 {
		t.Errorf("dispatch must not run for already-locked key; got %d", len(disp.reqs))
	}
}

func TestDispatchAgentTask_PropagatesDispatcherError(t *testing.T) {
	disp := &recordedDispatch{err: errors.New("boom")}
	notif := &recordedNotifier{}
	auth := &allowAuth{allowed: map[string]bool{"U0123": true}}
	store := newMemStore()
	svc := usecase.NewDispatchService(disp, notif, auth, store)
	req := domain.DispatchRequest{
		Role:           domain.AgentRolePaintress,
		Text:           "fix",
		RequesterID:    "U0123",
		IdempotencyKey: "k-err",
		IssuedAt:       1700000000,
	}
	target := port.NotifyTarget{CallbackURL: "https://hooks.slack.com/x", Mode: port.ModeSlack}

	err := svc.DispatchAgentTask(context.Background(), req, target)
	if err == nil {
		t.Fatal("expected dispatcher error to propagate, got nil")
	}
	// Released the lock so a retry would not be permanently blocked
	if !store.TryLock(req.OperationKey()) {
		t.Error("lock should be released after dispatcher error")
	}
}
