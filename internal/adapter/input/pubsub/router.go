package pubsub

import (
	"context"
	"errors"
	"fmt"

	"github.com/hironow/runops-gateway/internal/core/domain"
)

// OutboxRouter resolves a Writer for an incoming Pub/Sub message.
//
// Single-mode and multi-mode share the contract; only multi-mode
// inspects the project_id attribute, so single-mode preserves the
// pre-#0006 deployment behaviour byte-for-byte. The interface lives
// in input/pubsub (rather than output/phonewave) so the output adapter
// never imports input types — see ADR 0028 + plan v4.
type OutboxRouter interface {
	Resolve(ctx context.Context, m Message) (Writer, error)
}

// ErrProjectNotRouted signals that an incoming message's project_id is
// missing, malformed, or unmapped in the multi-mode router. Receivers
// nack the message so Pub/Sub's max_delivery_attempts shuttles it onto
// the dead-letter topic for operator triage.
var ErrProjectNotRouted = errors.New("project_id has no outbox mapping")

// SingleOutboxRouter wraps one writer; the message is read for nothing.
// Used by single-mode deployments (env PHONEWAVE_OUTBOX_DIR only) so the
// pre-#0006 behaviour is byte-for-byte preserved.
type SingleOutboxRouter struct {
	w Writer
}

// NewSingleOutboxRouter builds a router that ignores project_id.
func NewSingleOutboxRouter(w Writer) *SingleOutboxRouter {
	return &SingleOutboxRouter{w: w}
}

// Resolve returns the wrapped writer regardless of message contents.
// Intentionally does not touch m.Attributes() so a future grep can
// verify backward compat at a glance.
func (r *SingleOutboxRouter) Resolve(_ context.Context, _ Message) (Writer, error) {
	return r.w, nil
}

// MultiOutboxRouter routes by the project_id Pub/Sub attribute. Empty,
// malformed, or unknown values return ErrProjectNotRouted (callers nack
// to send the message to the DLQ). Used by multi-mode deployments
// (env PHONEWAVE_OUTBOX_DIRS_BY_PROJECT set).
type MultiOutboxRouter struct {
	writers map[string]Writer
}

// NewMultiOutboxRouter constructs a multi-mode router from an
// already-validated map (caller is responsible for ParseOutboxDirsByProject
// / NewOutboxWriter wiring upstream so init failures surface at process
// boot, not on first message).
func NewMultiOutboxRouter(writers map[string]Writer) *MultiOutboxRouter {
	return &MultiOutboxRouter{writers: writers}
}

// Resolve looks up the writer for m's project_id attribute. Returns
// ErrProjectNotRouted (wrapped with detail) when validation fails or
// the id is unmapped.
func (r *MultiOutboxRouter) Resolve(_ context.Context, m Message) (Writer, error) {
	pid := m.Attributes()["project_id"]
	if err := domain.ValidateProjectID(pid); err != nil {
		return nil, fmt.Errorf("%w: invalid project_id %q: %v", ErrProjectNotRouted, pid, err)
	}
	w, ok := r.writers[pid]
	if !ok {
		return nil, fmt.Errorf("%w: project_id %q not in router", ErrProjectNotRouted, pid)
	}
	return w, nil
}
