// Package pubsub adapts Cloud Pub/Sub Subscription.Receive into something the
// dmail-receiver daemon can drive. The Receiver type holds the message-handler
// logic so unit tests can drive it with fake messages without spinning up the
// SDK or the emulator; cmd/dmail-receiver wires it into a real
// *pubsub.Subscription.
package pubsub

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// attrEvent shortens span.AddEvent attribute construction (one of these per
// drop reason).
func attrEvent(k, v string) trace.EventOption {
	return trace.WithAttributes(attribute.String(k, v))
}

// receiverTracerName identifies this package as the OTel instrumentation
// library. Inbound subscriber spans (dmail-receiver daemon) live here.
const receiverTracerName = "github.com/hironow/runops-gateway/internal/adapter/input/pubsub"

// Message is the minimum surface Receiver requires from a Pub/Sub message.
// Mirrors the *pubsub.Message methods used by Subscription.Receive so the
// production wiring can implement it via a thin shim.
type Message interface {
	ID() string
	Data() []byte
	Attributes() map[string]string
	Ack()
	Nack()
}

// Writer is the outbox-write surface Receiver depends on. Production wires in
// *phonewave.OutboxWriter; tests use a recording fake.
type Writer interface {
	WriteFile(name string, data []byte) error
}

// Receiver bridges Pub/Sub messages to a phonewave outbox writer via an
// OutboxRouter. Single-mode deployments wire SingleOutboxRouter so the
// project_id attribute is ignored (backward compat); multi-mode wires
// MultiOutboxRouter so each project_id lands in its own outbox.
type Receiver struct {
	router OutboxRouter
}

// NewReceiver constructs a Receiver around the given OutboxRouter. The
// router decides whether project_id matters; the receiver itself is
// mode-agnostic (#0006 / ADR 0028).
func NewReceiver(r OutboxRouter) *Receiver {
	return &Receiver{router: r}
}

// OnMessage is the handler bound to Subscription.Receive. It writes the
// rendered D-Mail markdown to the outbox using the message ID as the filename
// stem (or the publisher-side `id` attribute if provided), then acks/nacks
// based on writer outcome.
//
// Empty data and bad-id attributes are acked-and-dropped because retrying
// would just reproduce the same shape — the producer needs to fix the bug.
func (r *Receiver) OnMessage(ctx context.Context, m Message) {
	ctx, span := otel.Tracer(receiverTracerName).Start(ctx, "dmail.receiver.on_message")
	defer span.End()
	span.SetAttributes(attribute.String("pubsub.message_id", m.ID()))

	data := m.Data()
	if len(data) == 0 {
		slog.WarnContext(ctx, "dmail receiver: empty data, dropping",
			"pubsub_message_id", m.ID())
		span.AddEvent("drop", attrEvent("reason", "empty_data"))
		m.Ack()
		return
	}

	name, err := chooseFilename(m)
	if err != nil {
		slog.WarnContext(ctx, "dmail receiver: invalid id attribute, dropping",
			"pubsub_message_id", m.ID(), "error", err)
		span.RecordError(err)
		span.AddEvent("drop", attrEvent("reason", "invalid_id"))
		m.Ack()
		return
	}
	span.SetAttributes(attribute.String("outbox.filename", name))

	writer, err := r.router.Resolve(ctx, m)
	if errors.Is(err, ErrProjectNotRouted) {
		// multi-mode rejected this project_id — silently drop would orphan
		// the message; instead nack so Pub/Sub max_delivery_attempts ships
		// it to the DLQ for operator triage (ADR 0028).
		slog.WarnContext(ctx, "dmail receiver: project_id not routed; nacking to DLQ",
			"pubsub_message_id", m.ID(),
			"project_id", m.Attributes()["project_id"],
			"error", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "project_not_routed")
		m.Nack()
		return
	}
	if err != nil {
		slog.ErrorContext(ctx, "dmail receiver: router resolve failed; nacking for retry",
			"pubsub_message_id", m.ID(), "error", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "router resolve failed")
		m.Nack()
		return
	}
	if pid := m.Attributes()["project_id"]; pid != "" {
		span.SetAttributes(attribute.String("project_id", pid))
	}

	if err := writer.WriteFile(name, data); err != nil {
		slog.ErrorContext(ctx, "dmail receiver: outbox write failed; nacking for retry",
			"pubsub_message_id", m.ID(), "name", name, "error", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "outbox write failed")
		m.Nack()
		return
	}
	slog.InfoContext(ctx, "dmail receiver: wrote outbox",
		"pubsub_message_id", m.ID(), "name", name)
	m.Ack()
}

// chooseFilename derives the on-disk filename. Prefers the publisher's `id`
// attribute (set by PubsubDispatcher to the D-Mail.ID); falls back to the
// Pub/Sub message ID. Always sanitized so the writer can never escape the
// outbox dir.
func chooseFilename(m Message) (string, error) {
	candidate := m.Attributes()["id"]
	if candidate == "" {
		candidate = m.ID()
	}
	if candidate == "" {
		return "", fmt.Errorf("no id available")
	}
	// Reject path traversal even before the writer's own validateName runs —
	// keeps the error path here so the test asserts the correct branch.
	if strings.ContainsAny(candidate, "/\\") || strings.Contains(candidate, "..") {
		return "", fmt.Errorf("id contains unsafe characters: %q", candidate)
	}
	if base := filepath.Base(candidate); base != candidate {
		return "", fmt.Errorf("id is not a single path segment: %q", candidate)
	}
	return candidate + ".md", nil
}
