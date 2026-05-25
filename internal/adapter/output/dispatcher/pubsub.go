package dispatcher

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"

	"github.com/hironow/runops-gateway/internal/core/domain"
	"github.com/hironow/runops-gateway/internal/core/port"
)

// PubsubDispatcher implements port.Dispatcher by translating a DispatchRequest
// into a D-Mail (kind=specification, target=role) and handing it off to the
// configured DMailPublisher.
//
// This is the Phase 2a backend Behind the same port.Dispatcher seam as
// StubDispatcher; cmd/server picks one or the other based on the
// DISPATCHER_BACKEND env var, so the use case layer stays unchanged.
type PubsubDispatcher struct {
	publisher port.DMailPublisher
}

// NewPubsubDispatcher constructs a PubsubDispatcher. publisher is typically a
// *pubsub.Publisher targeting the dmail-inbound topic; tests inject a
// recorder.
func NewPubsubDispatcher(publisher port.DMailPublisher) *PubsubDispatcher {
	return &PubsubDispatcher{publisher: publisher}
}

// Dispatch turns req into a DMail and publishes it. Returns the underlying
// publisher error verbatim (wrapped with context) so the use case can decide
// whether to surface it to the operator.
func (d *PubsubDispatcher) Dispatch(ctx context.Context, req domain.DispatchRequest) error {
	if req.Role == "" {
		return fmt.Errorf("pubsub dispatcher: DispatchRequest.Role is required")
	}

	metadata := map[string]string{
		"requester_id": req.RequesterID,
	}
	// Phase 3 (ADR 0018) propagates these so the outbound subscriber can
	// thread-reply into the right Slack message. Empty values are omitted
	// because the receiver uses presence-of-key as the routing signal.
	if req.SlackChannelID != "" {
		metadata["slack_channel_id"] = req.SlackChannelID
	}
	if req.SlackThreadTS != "" {
		metadata["slack_thread_ts"] = req.SlackThreadTS
	}
	if req.IdempotencyKey != "" {
		metadata["parent_idempotency_key"] = req.IdempotencyKey
	}
	// #0008 (ADR 0027): carry the multiplex project_id so dmail-receiver
	// can route to the correct workspace outbox and the 5 tools see the
	// project on every D-Mail. Absent / empty values are omitted to keep
	// existing non-multiplex deployments byte-identical.
	if req.ProjectID != "" {
		metadata["project_id"] = req.ProjectID
	}

	mail := domain.DMail{
		ID:             newDMailID(),
		Kind:           domain.DMailKindSpecification,
		Target:         string(req.Role),
		Source:         "runops-gateway-slack",
		IdempotencyKey: req.IdempotencyKey,
		Body:           req.Text,
		Metadata:       metadata,
	}

	id, err := d.publisher.PublishDMail(ctx, mail)
	if err != nil {
		return fmt.Errorf("pubsub dispatcher: publish: %w", err)
	}
	slog.InfoContext(ctx, "dispatched via pubsub",
		"role", req.Role,
		"requester_id", req.RequesterID,
		"idempotency_key", req.IdempotencyKey,
		"pubsub_message_id", id,
	)
	return nil
}

// newDMailID returns a 16-byte hex string used as the D-Mail's ID and the
// receiver-side filename stem. crypto/rand keeps it collision-free across
// concurrent operators; on the rare entropy failure we fall back to a marker
// so the publish still proceeds (dedup degrades, behavior remains).
func newDMailID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "rand-fallback"
	}
	return hex.EncodeToString(b)
}
