package pubsub

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/hironow/runops-gateway/internal/core/domain"
	"github.com/hironow/runops-gateway/internal/core/port"
)

// recordingPublisher captures every publishMessage handed to PublishDMail.
// Uses the publisher's own internal type so production and test share the
// same envelope shape.
type recordingPublisher struct {
	posts    []publishMessage
	resultID string
	err      error
}

func (r *recordingPublisher) publish(_ context.Context, msg publishMessage) (string, error) {
	r.posts = append(r.posts, msg)
	if r.err != nil {
		return "", r.err
	}
	if r.resultID == "" {
		return "msg-1", nil
	}
	return r.resultID, nil
}

// compile-time interface assertion
var _ port.DMailPublisher = (*Publisher)(nil)

func TestPublisher_PublishDMail_AttachesADRAttributes(t *testing.T) {
	rec := &recordingPublisher{resultID: "abc-123"}
	p := newTestPublisher(rec.publish)

	dm := domain.DMail{
		ID:             "01HZW0K0AB12CD34EF56GH78JK",
		Kind:           domain.DMailKindSpecification,
		Target:         "paintress",
		Source:         "runops-gateway-slack",
		IdempotencyKey: "abcd1234",
		Body:           "Fix M-42.",
		Metadata: map[string]string{
			"requester_id": "U0123ABCD",
			"traceparent":  "00-deadbeefcafe-0000000000000000-01",
		},
	}

	id, err := p.PublishDMail(context.Background(), dm)
	if err != nil {
		t.Fatalf("PublishDMail returned error: %v", err)
	}
	if id != "abc-123" {
		t.Errorf("expected message id abc-123, got %q", id)
	}
	if len(rec.posts) != 1 {
		t.Fatalf("expected exactly one publish call, got %d", len(rec.posts))
	}
	got := rec.posts[0]

	wantAttrs := map[string]string{
		"kind":                 "specification",
		"target_tool":          "paintress",
		"source":               "runops-gateway-slack",
		"dmail_schema_version": "1",
		"idempotency_key":      "abcd1234",
		"traceparent":          "00-deadbeefcafe-0000000000000000-01",
	}
	for k, want := range wantAttrs {
		if got.Attributes[k] != want {
			t.Errorf("attribute %s: got %q want %q", k, got.Attributes[k], want)
		}
	}
}

func TestPublisher_PublishDMail_DataContainsRenderedMarkdown(t *testing.T) {
	rec := &recordingPublisher{}
	p := newTestPublisher(rec.publish)

	dm := domain.DMail{
		ID:             "01HZW",
		Kind:           domain.DMailKindReport,
		Target:         "amadeus",
		Source:         "runops-gateway-slack",
		IdempotencyKey: "k1",
		Body:           "PR #42 merged.",
	}
	if _, err := p.PublishDMail(context.Background(), dm); err != nil {
		t.Fatalf("PublishDMail: %v", err)
	}
	doc := string(rec.posts[0].Data)
	for _, want := range []string{
		"dmail-schema-version: \"1\"",
		"kind: report",
		"target: amadeus",
		"PR #42 merged.",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("rendered data missing %q; got:\n%s", want, doc)
		}
	}
}

func TestPublisher_PublishDMail_OrderingKeyIsTarget(t *testing.T) {
	// Slack-issued retries for the same target should preserve order so the
	// receiver sees specifications before reports etc. Use target_tool as the
	// ordering key — distinct tools get parallel ordering, same tool serializes.
	rec := &recordingPublisher{}
	p := newTestPublisher(rec.publish)

	dm := domain.DMail{
		ID:             "01HZW",
		Kind:           domain.DMailKindSpecification,
		Target:         "paintress",
		IdempotencyKey: "k",
		Body:           "do",
	}
	_, _ = p.PublishDMail(context.Background(), dm)
	if rec.posts[0].OrderingKey != "paintress" {
		t.Errorf("expected OrderingKey=paintress, got %q", rec.posts[0].OrderingKey)
	}
}

func TestPublisher_PublishDMail_PropagatesPublishError(t *testing.T) {
	rec := &recordingPublisher{err: errors.New("topic does not exist")}
	p := newTestPublisher(rec.publish)

	dm := domain.DMail{
		ID:     "01HZW",
		Kind:   domain.DMailKindSpecification,
		Target: "paintress",
		Body:   "x",
	}
	_, err := p.PublishDMail(context.Background(), dm)
	if err == nil {
		t.Fatal("expected error to propagate")
	}
	if !strings.Contains(err.Error(), "topic does not exist") {
		t.Errorf("error should wrap underlying message; got: %v", err)
	}
}

func TestPublisher_PublishDMail_RejectsZeroValue(t *testing.T) {
	// Empty Kind / Target are programmer mistakes — fail loud.
	rec := &recordingPublisher{}
	p := newTestPublisher(rec.publish)

	if _, err := p.PublishDMail(context.Background(), domain.DMail{}); err == nil {
		t.Error("expected error for zero-value DMail, got nil")
	}
}
