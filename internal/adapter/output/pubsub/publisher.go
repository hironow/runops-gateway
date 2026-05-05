// Package pubsub implements port.DMailPublisher backed by Google Cloud Pub/Sub.
//
// Production wiring uses cloud.google.com/go/pubsub (v1) which is what async
// patterns elsewhere in the org standardize on. The publish call itself is
// abstracted behind publishFunc so unit tests can inject a fake and avoid
// pulling the SDK into the test dependency graph.
//
// Local development can target the Firebase Pub/Sub emulator by setting
// PUBSUB_EMULATOR_HOST (the SDK auto-detects it). See docs/local-verification.md
// for the recommended docker-compose setup adapted from /Users/nino/ai-code/async.
package pubsub

import (
	"context"
	"fmt"
	"log/slog"

	gpubsub "cloud.google.com/go/pubsub/v2"

	"github.com/hironow/runops-gateway/internal/core/domain"
)

// publishMessage is the internal envelope that carries the rendered D-Mail to
// the publish layer. Mirrors the subset of *pubsub.Message we actually use so
// publishFunc stays SDK-free for tests.
type publishMessage struct {
	Data        []byte
	Attributes  map[string]string
	OrderingKey string
}

// publishFunc is the indirection between Publisher and the underlying
// transport. Production binds it to a real Pub/Sub topic; tests inject a
// recorder.
type publishFunc func(ctx context.Context, msg publishMessage) (string, error)

// Publisher implements port.DMailPublisher.
type Publisher struct {
	publish   publishFunc
	client    *gpubsub.Client    // nil for test publishers; closed by Close()
	publisher *gpubsub.Publisher // nil for test publishers; v2 returns this from client.Publisher(topic)
}

// NewPublisher returns a Pub/Sub-backed Publisher targeting projectID/topicID.
// Ordering is enabled because PublishDMail uses target_tool as the ordering
// key (so two D-Mails for the same target stay serialized — see ADR 0013).
func NewPublisher(ctx context.Context, projectID, topicID string) (*Publisher, error) {
	if projectID == "" || topicID == "" {
		return nil, fmt.Errorf("pubsub publisher: projectID and topicID are required")
	}
	client, err := gpubsub.NewClient(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("pubsub publisher: create client: %w", err)
	}
	pub := client.Publisher(topicID)
	pub.EnableMessageOrdering = true
	p := &Publisher{client: client, publisher: pub}
	p.publish = func(ctx context.Context, msg publishMessage) (string, error) {
		result := pub.Publish(ctx, &gpubsub.Message{
			Data:        msg.Data,
			Attributes:  msg.Attributes,
			OrderingKey: msg.OrderingKey,
		})
		return result.Get(ctx)
	}
	return p, nil
}

// newTestPublisher is a constructor used only by package tests.
func newTestPublisher(fn publishFunc) *Publisher {
	return &Publisher{publish: fn}
}

// Close releases the underlying Pub/Sub client. Safe to call on test
// publishers (no-op).
func (p *Publisher) Close() error {
	if p.publisher != nil {
		p.publisher.Stop()
	}
	if p.client != nil {
		return p.client.Close()
	}
	return nil
}

// PublishDMail validates m, builds the Pub/Sub envelope per ADR 0013, and
// hands it off to the configured publishFunc. Returns the publisher-assigned
// message ID on success.
func (p *Publisher) PublishDMail(ctx context.Context, m domain.DMail) (string, error) {
	if m.Kind == "" || m.Target == "" {
		return "", fmt.Errorf("pubsub publisher: DMail.Kind and DMail.Target are required (got kind=%q target=%q)",
			m.Kind, m.Target)
	}

	attrs := map[string]string{
		"kind":                 string(m.Kind),
		"target_tool":          m.Target,
		"dmail_schema_version": "1",
	}
	if m.Source != "" {
		attrs["source"] = m.Source
	}
	if m.IdempotencyKey != "" {
		attrs["idempotency_key"] = m.IdempotencyKey
	}
	// Metadata may carry traceparent and other ADR 0013 attributes. We forward
	// them verbatim so the receiver can stitch traces and consult provenance.
	for k, v := range m.Metadata {
		// Avoid clobbering canonical keys we set above.
		if _, exists := attrs[k]; exists {
			continue
		}
		attrs[k] = v
	}

	msg := publishMessage{
		Data:        []byte(m.RenderMarkdown()),
		Attributes:  attrs,
		OrderingKey: m.Target, // serialize per-target so receiver order is preserved
	}

	id, err := p.publish(ctx, msg)
	if err != nil {
		return "", fmt.Errorf("pubsub publisher: publish kind=%s target=%s: %w", m.Kind, m.Target, err)
	}
	slog.InfoContext(ctx, "pubsub publish", "id", id, "kind", m.Kind, "target", m.Target,
		"idempotency_key", m.IdempotencyKey)
	return id, nil
}
