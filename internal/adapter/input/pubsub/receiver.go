// Package pubsub adapts Cloud Pub/Sub Subscription.Receive into something the
// dmail-receiver daemon can drive. The Receiver type holds the message-handler
// logic so unit tests can drive it with fake messages without spinning up the
// SDK or the emulator; cmd/dmail-receiver wires it into a real
// *pubsub.Subscription.
package pubsub

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
)

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

// Receiver bridges Pub/Sub messages to a phonewave outbox writer.
type Receiver struct {
	writer Writer
}

// NewReceiver constructs a Receiver around the given Writer.
func NewReceiver(w Writer) *Receiver {
	return &Receiver{writer: w}
}

// OnMessage is the handler bound to Subscription.Receive. It writes the
// rendered D-Mail markdown to the outbox using the message ID as the filename
// stem (or the publisher-side `id` attribute if provided), then acks/nacks
// based on writer outcome.
//
// Empty data and bad-id attributes are acked-and-dropped because retrying
// would just reproduce the same shape — the producer needs to fix the bug.
func (r *Receiver) OnMessage(ctx context.Context, m Message) {
	data := m.Data()
	if len(data) == 0 {
		slog.WarnContext(ctx, "dmail receiver: empty data, dropping",
			"pubsub_message_id", m.ID())
		m.Ack()
		return
	}

	name, err := chooseFilename(m)
	if err != nil {
		slog.WarnContext(ctx, "dmail receiver: invalid id attribute, dropping",
			"pubsub_message_id", m.ID(), "error", err)
		m.Ack()
		return
	}

	if err := r.writer.WriteFile(name, data); err != nil {
		slog.ErrorContext(ctx, "dmail receiver: outbox write failed; nacking for retry",
			"pubsub_message_id", m.ID(), "name", name, "error", err)
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
