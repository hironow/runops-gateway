package pubsub

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/hironow/runops-gateway/internal/core/domain"
)

// ResultHandler is the use case surface OutboundReceiver hands parsed D-Mails
// to. Production wires DispatchResultHandler from internal/usecase; tests
// inject a recorder.
type ResultHandler interface {
	Handle(ctx context.Context, mail domain.DMail) error
}

// OutboundReceiver bridges Pub/Sub messages on the dmail-outbound topic to
// the result-handling use case. Symmetric with Receiver (dmail-inbound) but:
//
//   - parses the message data into a domain.DMail before handing off (the
//     inbound side just writes the raw bytes verbatim into a phonewave
//     outbox)
//   - delegates Slack thread routing to the handler so this adapter stays
//     transport-only and unit-testable without the SDK
type OutboundReceiver struct {
	handler ResultHandler
}

// NewOutboundReceiver returns a receiver that drives handler on every parsed
// message.
func NewOutboundReceiver(handler ResultHandler) *OutboundReceiver {
	return &OutboundReceiver{handler: handler}
}

// OnMessage is the function bound to Subscription.Receive. ack semantics:
//
//   - empty or unparseable data → ack-and-drop (retrying produces the same
//     bad shape; the producer needs to be fixed)
//   - handler returns nil (success or routing-skip) → ack
//   - handler returns error → nack so Pub/Sub redelivers; eventual DLQ
//     forwarding kicks in via the subscription's dead_letter_policy
func (r *OutboundReceiver) OnMessage(ctx context.Context, m Message) {
	ctx, span := otel.Tracer(receiverTracerName).Start(ctx, "dmail.outbound.on_message")
	defer span.End()
	span.SetAttributes(attribute.String("pubsub.message_id", m.ID()))

	data := m.Data()
	if len(data) == 0 {
		slog.WarnContext(ctx, "outbound subscriber: empty data, dropping",
			"pubsub_message_id", m.ID())
		span.AddEvent("drop", attrEvent("reason", "empty_data"))
		m.Ack()
		return
	}
	mail, err := domain.ParseDMail(data)
	if err != nil {
		slog.WarnContext(ctx, "outbound subscriber: invalid D-Mail, dropping",
			"pubsub_message_id", m.ID(), "error", err)
		span.RecordError(err)
		span.AddEvent("drop", attrEvent("reason", "parse_failed"))
		m.Ack()
		return
	}
	span.SetAttributes(
		attribute.String("dmail.kind", string(mail.Kind)),
		attribute.String("dmail.target", mail.Target),
	)
	if err := r.handler.Handle(ctx, mail); err != nil {
		slog.ErrorContext(ctx, "outbound subscriber: handler failed; nacking for retry",
			"pubsub_message_id", m.ID(), "kind", mail.Kind, "error", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "handler failed")
		m.Nack()
		return
	}
	slog.InfoContext(ctx, "outbound subscriber: handled",
		"pubsub_message_id", m.ID(), "kind", mail.Kind, "target", mail.Target)
	m.Ack()
}
