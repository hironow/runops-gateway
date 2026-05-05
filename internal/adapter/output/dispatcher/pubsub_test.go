package dispatcher

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/hironow/runops-gateway/internal/core/domain"
)

// recordingDMailPublisher captures every PublishDMail call.
type recordingDMailPublisher struct {
	mu       sync.Mutex
	mails    []domain.DMail
	resultID string
	err      error
}

func (r *recordingDMailPublisher) PublishDMail(_ context.Context, m domain.DMail) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.mails = append(r.mails, m)
	if r.err != nil {
		return "", r.err
	}
	if r.resultID == "" {
		return "msg-id-1", nil
	}
	return r.resultID, nil
}

func (r *recordingDMailPublisher) snapshot() []domain.DMail {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]domain.DMail, len(r.mails))
	copy(out, r.mails)
	return out
}

func TestPubsubDispatcher_PropagatesSlackThreadMetadataForPhase3(t *testing.T) {
	// Phase 3 (ADR 0018) needs slack_channel_id / slack_thread_ts /
	// parent_idempotency_key to travel through Pub/Sub so the outbound
	// subscriber can route results back to the originating Slack thread.
	pub := &recordingDMailPublisher{}
	d := NewPubsubDispatcher(pub)

	req := domain.DispatchRequest{
		Role:           domain.AgentRolePaintress,
		Text:           "fix M-42",
		RequesterID:    "U0123ABCD",
		IdempotencyKey: "phase3-key",
		IssuedAt:       1700000000,
		SlackChannelID: "C0SLACKCH",
		SlackThreadTS:  "1700000000.000050",
	}
	if err := d.Dispatch(context.Background(), req); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	mails := pub.snapshot()
	if len(mails) != 1 {
		t.Fatalf("expected 1 publish, got %d", len(mails))
	}
	want := map[string]string{
		"requester_id":           "U0123ABCD",
		"slack_channel_id":       "C0SLACKCH",
		"slack_thread_ts":        "1700000000.000050",
		"parent_idempotency_key": "phase3-key",
	}
	for k, v := range want {
		if mails[0].Metadata[k] != v {
			t.Errorf("metadata[%s]=%q want %q (full metadata=%v)",
				k, mails[0].Metadata[k], v, mails[0].Metadata)
		}
	}
}

func TestPubsubDispatcher_OmitsSlackMetadataForCLIDispatch(t *testing.T) {
	// CLI dispatch (no Slack origin) must not emit empty metadata keys —
	// the receiver uses presence-of-key as the signal that a message
	// originated from Slack and should be threaded back.
	pub := &recordingDMailPublisher{}
	d := NewPubsubDispatcher(pub)

	req := domain.DispatchRequest{
		Role:           domain.AgentRoleAmadeus,
		Text:           "scan",
		RequesterID:    "operator@example.com",
		IdempotencyKey: "cli-1",
		IssuedAt:       1700000000,
		// SlackChannelID + SlackThreadTS intentionally empty
	}
	if err := d.Dispatch(context.Background(), req); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	got := pub.snapshot()[0].Metadata
	for _, key := range []string{"slack_channel_id", "slack_thread_ts"} {
		if _, exists := got[key]; exists {
			t.Errorf("metadata must omit %s for CLI dispatches; got %q", key, got[key])
		}
	}
}

func TestPubsubDispatcher_TranslatesDispatchRequestToDMail(t *testing.T) {
	pub := &recordingDMailPublisher{}
	d := NewPubsubDispatcher(pub)

	req := domain.DispatchRequest{
		Role:           domain.AgentRolePaintress,
		Text:           "Fix M-42",
		RequesterID:    "U0123ABCD",
		IdempotencyKey: "abcd1234",
		IssuedAt:       1700000000,
	}

	if err := d.Dispatch(context.Background(), req); err != nil {
		t.Fatalf("Dispatch returned error: %v", err)
	}
	mails := pub.snapshot()
	if len(mails) != 1 {
		t.Fatalf("expected exactly one DMail published, got %d", len(mails))
	}
	m := mails[0]
	if m.Kind != domain.DMailKindSpecification {
		t.Errorf("Kind=%q, want specification", m.Kind)
	}
	if m.Target != "paintress" {
		t.Errorf("Target=%q, want paintress", m.Target)
	}
	if m.Source != "runops-gateway-slack" {
		t.Errorf("Source=%q, want runops-gateway-slack", m.Source)
	}
	if m.IdempotencyKey != "abcd1234" {
		t.Errorf("IdempotencyKey=%q", m.IdempotencyKey)
	}
	if m.ID == "" {
		t.Error("DMail.ID should be assigned (used as filename stem on receiver side)")
	}
	if m.Body == "" || !strings.Contains(m.Body, "Fix M-42") {
		t.Errorf("Body should embed the dispatch text; got: %q", m.Body)
	}
	// Metadata must carry the requester for ADR 0012 sender attribution.
	if m.Metadata["requester_id"] != "U0123ABCD" {
		t.Errorf("Metadata.requester_id=%q, want U0123ABCD", m.Metadata["requester_id"])
	}
}

func TestPubsubDispatcher_RejectsZeroRole(t *testing.T) {
	pub := &recordingDMailPublisher{}
	d := NewPubsubDispatcher(pub)
	if err := d.Dispatch(context.Background(), domain.DispatchRequest{
		RequesterID: "U0001",
		Text:        "x",
	}); err == nil {
		t.Error("expected error for empty Role, got nil")
	}
	if got := pub.snapshot(); len(got) != 0 {
		t.Errorf("publisher must not be called when validation fails; got %d", len(got))
	}
}

func TestPubsubDispatcher_PropagatesPublishError(t *testing.T) {
	pub := &recordingDMailPublisher{err: errors.New("topic does not exist")}
	d := NewPubsubDispatcher(pub)
	req := domain.DispatchRequest{
		Role:           domain.AgentRoleSightjack,
		Text:           "scan",
		RequesterID:    "U0002",
		IdempotencyKey: "k",
	}
	err := d.Dispatch(context.Background(), req)
	if err == nil {
		t.Fatal("expected publish error to propagate")
	}
	if !strings.Contains(err.Error(), "topic does not exist") {
		t.Errorf("error should wrap publisher error: %v", err)
	}
}

// compile-time interface assertion is in stub_test.go via *StubDispatcher; the
// PubsubDispatcher case lives here so this file compiles standalone.
