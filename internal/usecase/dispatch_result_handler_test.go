package usecase_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/hironow/runops-gateway/internal/core/domain"
	"github.com/hironow/runops-gateway/internal/core/port"
	"github.com/hironow/runops-gateway/internal/usecase"
)

// recordingThreadNotifier captures every UpdateMessage call along with the
// NotifyTarget it was sent to so tests can assert channel + thread routing.
type recordingThreadNotifier struct {
	mu      sync.Mutex
	updates []notifyCall
	err     error
}

type notifyCall struct {
	Target port.NotifyTarget
	Text   string
}

func (n *recordingThreadNotifier) UpdateMessage(_ context.Context, t port.NotifyTarget, text string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.updates = append(n.updates, notifyCall{Target: t, Text: text})
	return n.err
}
func (n *recordingThreadNotifier) ReplaceMessage(_ context.Context, _ port.NotifyTarget, _ string) error {
	return nil
}
func (n *recordingThreadNotifier) SendEphemeral(_ context.Context, _ port.NotifyTarget, _, _ string) error {
	return nil
}
func (n *recordingThreadNotifier) OfferContinuation(_ context.Context, _ port.NotifyTarget, _ string, _, _ *domain.ApprovalRequest) error {
	return nil
}
func (n *recordingThreadNotifier) RebuildInitialApproval(_ context.Context, _ port.NotifyTarget, _ string, _, _, _ *domain.ApprovalRequest) error {
	return nil
}

func (n *recordingThreadNotifier) snapshot() []notifyCall {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]notifyCall, len(n.updates))
	copy(out, n.updates)
	return out
}

func makeDMail(kind domain.DMailKind, body string, withSlackMeta bool) domain.DMail {
	m := domain.DMail{
		ID:             "dm-1",
		Kind:           kind,
		Target:         "amadeus",
		Source:         "paintress",
		IdempotencyKey: "k1",
		Body:           body,
		Metadata:       map[string]string{},
	}
	if withSlackMeta {
		m.Metadata["slack_channel_id"] = "C123"
		m.Metadata["slack_thread_ts"] = "1700000000.000050"
		m.Metadata["parent_idempotency_key"] = "parent-k"
	}
	return m
}

func TestDispatchResultHandler_Report_PostsToSlackThread(t *testing.T) {
	notif := &recordingThreadNotifier{}
	h := usecase.NewDispatchResultHandler(notif)

	mail := makeDMail(domain.DMailKindReport, "PR #42 merged.", true)
	if err := h.Handle(context.Background(), mail); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	got := notif.snapshot()
	if len(got) != 1 {
		t.Fatalf("expected 1 UpdateMessage, got %d", len(got))
	}
	if got[0].Target.ChannelID != "C123" || got[0].Target.ThreadTS != "1700000000.000050" {
		t.Errorf("target routing wrong: %+v", got[0].Target)
	}
	if !strings.Contains(got[0].Text, "PR #42 merged.") {
		t.Errorf("text should include body; got %q", got[0].Text)
	}
	// kind- or source-specific marker so operators can tell what kind it was.
	if !strings.Contains(got[0].Text, "amadeus") && !strings.Contains(got[0].Text, "paintress") {
		t.Errorf("text should mention target or source; got %q", got[0].Text)
	}
}

func TestDispatchResultHandler_KindSpecificFormatting(t *testing.T) {
	cases := []struct {
		kind     domain.DMailKind
		mustHave string
	}{
		{domain.DMailKindReport, "✅"},
		{domain.DMailKindDesignFeedback, "🎨"},
		{domain.DMailKindImplementationFeedback, "🔧"},
		{domain.DMailKindConvergence, "🌐"},
		{domain.DMailKindCIResult, "🚦"},
	}
	for _, tc := range cases {
		t.Run(string(tc.kind), func(t *testing.T) {
			notif := &recordingThreadNotifier{}
			h := usecase.NewDispatchResultHandler(notif)
			mail := makeDMail(tc.kind, "marker-body", true)
			if err := h.Handle(context.Background(), mail); err != nil {
				t.Fatalf("Handle: %v", err)
			}
			got := notif.snapshot()
			if len(got) != 1 {
				t.Fatalf("expected 1 update, got %d", len(got))
			}
			if !strings.Contains(got[0].Text, tc.mustHave) {
				t.Errorf("kind=%s text should contain %q; got %q", tc.kind, tc.mustHave, got[0].Text)
			}
		})
	}
}

func TestDispatchResultHandler_DropsWhenSlackMetadataMissing(t *testing.T) {
	// Without parent_idempotency_key + slack_channel_id + slack_thread_ts the
	// handler cannot route the result back to a Slack thread, so it returns
	// nil (caller acks) without invoking the notifier.
	notif := &recordingThreadNotifier{}
	h := usecase.NewDispatchResultHandler(notif)

	mail := makeDMail(domain.DMailKindReport, "result without thread", false)
	if err := h.Handle(context.Background(), mail); err != nil {
		t.Fatalf("Handle should succeed (drop+ack), got: %v", err)
	}
	if got := notif.snapshot(); len(got) != 0 {
		t.Errorf("notifier must not be called when slack metadata missing; got %d", len(got))
	}
}

func TestDispatchResultHandler_PropagatesNotifierError(t *testing.T) {
	notif := &recordingThreadNotifier{err: errors.New("slack down")}
	h := usecase.NewDispatchResultHandler(notif)

	mail := makeDMail(domain.DMailKindReport, "x", true)
	err := h.Handle(context.Background(), mail)
	if err == nil {
		t.Fatal("expected notifier error to propagate (so caller can nack)")
	}
}

// recordingApprovalRequester captures every PostApprovalRequest call so
// Phase 4a tests can assert the HIGH severity convergence routing.
type recordingApprovalRequester struct {
	mu      sync.Mutex
	mails   []domain.DMail
	targets []port.NotifyTarget
	err     error
}

func (r *recordingApprovalRequester) PostApprovalRequest(_ context.Context, target port.NotifyTarget, mail domain.DMail) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.mails = append(r.mails, mail)
	r.targets = append(r.targets, target)
	return r.err
}

func (r *recordingApprovalRequester) snapshot() ([]domain.DMail, []port.NotifyTarget) {
	r.mu.Lock()
	defer r.mu.Unlock()
	mails := append([]domain.DMail{}, r.mails...)
	targets := append([]port.NotifyTarget{}, r.targets...)
	return mails, targets
}

func TestDispatchResultHandler_HighSeverityConvergence_PostsApprovalRequest(t *testing.T) {
	notif := &recordingThreadNotifier{}
	approver := &recordingApprovalRequester{}
	h := usecase.NewDispatchResultHandler(notif).WithApprovalRequester(approver)

	mail := makeDMail(domain.DMailKindConvergence, "ADR-003 violation", true)
	mail.Source = "amadeus"
	mail.Target = "sightjack"
	mail.Metadata["severity"] = "high"
	mail.Metadata["requester_id"] = "U_ORIG_DISPATCH"

	if err := h.Handle(context.Background(), mail); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	mails, targets := approver.snapshot()
	if len(mails) != 1 {
		t.Fatalf("expected 1 approval request, got %d", len(mails))
	}
	if mails[0].Source != "amadeus" || mails[0].Target != "sightjack" {
		t.Errorf("approval mail fields drifted: %+v", mails[0])
	}
	if targets[0].ChannelID != "C123" || targets[0].ThreadTS != "1700000000.000050" {
		t.Errorf("approval target routing wrong: %+v", targets[0])
	}
	if got := notif.snapshot(); len(got) != 0 {
		t.Errorf("regular thread reply must be suppressed for HIGH severity convergence; got %d updates", len(got))
	}
}

func TestDispatchResultHandler_NonHighConvergence_FallsBackToTextReply(t *testing.T) {
	notif := &recordingThreadNotifier{}
	approver := &recordingApprovalRequester{}
	h := usecase.NewDispatchResultHandler(notif).WithApprovalRequester(approver)

	mail := makeDMail(domain.DMailKindConvergence, "minor warning", true)
	mail.Metadata["severity"] = "low"

	if err := h.Handle(context.Background(), mail); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if got, _ := approver.snapshot(); len(got) != 0 {
		t.Errorf("approval requester must not fire for low-severity convergence; got %d", len(got))
	}
	if got := notif.snapshot(); len(got) != 1 {
		t.Errorf("low-severity convergence should still post a regular thread reply; got %d", len(got))
	}
}

func TestDispatchResultHandler_HighSeverity_FallsBackWhenApprovalRequesterNil(t *testing.T) {
	notif := &recordingThreadNotifier{}
	// Do NOT call WithApprovalRequester — simulates a deployment that has not
	// wired Phase 4a yet. Behavior must degrade gracefully (regular reply).
	h := usecase.NewDispatchResultHandler(notif)

	mail := makeDMail(domain.DMailKindConvergence, "high but no approver wired", true)
	mail.Metadata["severity"] = "high"

	if err := h.Handle(context.Background(), mail); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if got := notif.snapshot(); len(got) != 1 {
		t.Errorf("missing ApprovalRequester should fall back to plain thread reply; got %d", len(got))
	}
}

func TestDispatchResultHandler_RejectsUnknownKind(t *testing.T) {
	// Defensive: if a future producer slips a kind we do not format yet,
	// return nil + drop so we do not nack-loop forever in production.
	// Equivalent to "ack and surface a warning log".
	notif := &recordingThreadNotifier{}
	h := usecase.NewDispatchResultHandler(notif)

	mail := makeDMail(domain.DMailKind("unknown-kind-future"), "x", true)
	if err := h.Handle(context.Background(), mail); err != nil {
		t.Errorf("unknown kind should ack-drop, got error: %v", err)
	}
	if got := notif.snapshot(); len(got) != 0 {
		t.Errorf("unknown kind must not invoke notifier; got %d calls", len(got))
	}
}
