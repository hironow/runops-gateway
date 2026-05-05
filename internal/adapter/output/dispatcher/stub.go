// Package dispatcher provides Dispatcher port implementations for the
// dispatch_agent_task use case.
//
// Phase 1 ships only StubDispatcher, which logs the request and returns nil.
// Phase 2 will add a PubsubDispatcher that publishes to the dmail-inbound
// Pub/Sub topic; both implementations satisfy port.Dispatcher so the use case
// is unchanged when the swap happens.
package dispatcher

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"

	"github.com/hironow/runops-gateway/internal/core/domain"
)

// textFingerprintLen is the number of hex characters of SHA-256 retained as a
// log-safe fingerprint of the dispatch text. 8 chars (32 bits) is enough to
// correlate two log lines from the same dispatch without leaking content.
const textFingerprintLen = 8

// textFingerprint returns the first textFingerprintLen hex characters of the
// SHA-256 of s. Used to log a non-reversible identifier instead of the raw
// dispatch text (ADR 0014 / Phase 1 review F-4 — secrets in /agent input
// must not be persisted to Cloud Logging).
func textFingerprint(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])[:textFingerprintLen]
}

// StubDispatcher implements port.Dispatcher by emitting a structured log line.
// It is intentionally side-effect free beyond logging so that Phase 1 can
// verify the Slack -> use case wiring end to end without touching Pub/Sub or
// the five pillars.
type StubDispatcher struct {
	logger *slog.Logger
}

// NewStubDispatcher returns a StubDispatcher. If logger is nil, slog.Default is used.
func NewStubDispatcher(logger *slog.Logger) *StubDispatcher {
	if logger == nil {
		logger = slog.Default()
	}
	return &StubDispatcher{logger: logger}
}

// Dispatch logs the request fields. Returns an error only when the request is
// missing a Role — every other validation belongs to the use case layer.
func (d *StubDispatcher) Dispatch(ctx context.Context, req domain.DispatchRequest) error {
	if req.Role == "" {
		return fmt.Errorf("stub dispatcher: DispatchRequest.Role is required")
	}
	d.logger.LogAttrs(ctx, slog.LevelInfo, "dispatched stub",
		slog.String("role", string(req.Role)),
		slog.Int("text_len", len(req.Text)),
		slog.String("text_sha256", textFingerprint(req.Text)),
		slog.String("requester_id", req.RequesterID),
		slog.String("idempotency_key", req.IdempotencyKey),
		slog.Int64("issued_at", req.IssuedAt),
	)
	return nil
}
