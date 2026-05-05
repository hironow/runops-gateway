package pubsub

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/hironow/runops-gateway/internal/core/domain"
)

// recordingResultHandler records every Handle call.
type recordingResultHandler struct {
	mu    sync.Mutex
	mails []domain.DMail
	err   error
}

func (r *recordingResultHandler) Handle(_ context.Context, m domain.DMail) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.mails = append(r.mails, m)
	return r.err
}

func (r *recordingResultHandler) snapshot() []domain.DMail {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]domain.DMail, len(r.mails))
	copy(out, r.mails)
	return out
}

func TestOutboundReceiver_OnMessage_ParsesAndHandsToHandler(t *testing.T) {
	hdl := &recordingResultHandler{}
	r := NewOutboundReceiver(hdl)

	mail := domain.DMail{
		ID:             "id-1",
		Kind:           domain.DMailKindReport,
		Target:         "amadeus",
		Source:         "paintress",
		IdempotencyKey: "k1",
		Body:           "PR #42 merged.",
		Metadata: map[string]string{
			"slack_channel_id":       "C123",
			"slack_thread_ts":        "1700000000.000050",
			"parent_idempotency_key": "parent-k",
		},
	}
	msg := &fakeMessage{
		id:         "pubsub-1",
		data:       []byte(mail.RenderMarkdown()),
		attributes: map[string]string{"kind": "report"},
	}
	r.OnMessage(context.Background(), msg)

	got := hdl.snapshot()
	if len(got) != 1 {
		t.Fatalf("expected 1 handle, got %d", len(got))
	}
	if got[0].Kind != domain.DMailKindReport || got[0].Target != "amadeus" {
		t.Errorf("parsed mail wrong: %+v", got[0])
	}
	if got[0].Metadata["slack_channel_id"] != "C123" {
		t.Errorf("metadata lost: %v", got[0].Metadata)
	}
	if !msg.acked {
		t.Error("message should be acked after successful handle")
	}
}

func TestOutboundReceiver_OnMessage_NacksOnHandlerError(t *testing.T) {
	hdl := &recordingResultHandler{err: errors.New("slack down")}
	r := NewOutboundReceiver(hdl)

	mail := domain.DMail{
		Kind: domain.DMailKindReport, Target: "amadeus",
		Body: "x",
		Metadata: map[string]string{
			"slack_channel_id":       "C",
			"slack_thread_ts":        "T",
			"parent_idempotency_key": "P",
		},
	}
	msg := &fakeMessage{id: "p-2", data: []byte(mail.RenderMarkdown())}
	r.OnMessage(context.Background(), msg)

	if !msg.nacked {
		t.Error("handler error must lead to nack so Pub/Sub retries")
	}
	if msg.acked {
		t.Error("must not ack a failed handle")
	}
}

func TestOutboundReceiver_OnMessage_AcksOnEmptyOrUnparseable(t *testing.T) {
	hdl := &recordingResultHandler{}
	r := NewOutboundReceiver(hdl)

	for name, msg := range map[string]*fakeMessage{
		"empty data":           {id: "e", data: nil},
		"not-a-dmail":          {id: "g", data: []byte("not a dmail")},
		"unclosed frontmatter": {id: "u", data: []byte("---\nkind: report\nbody")},
	} {
		t.Run(name, func(t *testing.T) {
			msg.acked = false
			msg.nacked = false
			r.OnMessage(context.Background(), msg)
			if !msg.acked {
				t.Errorf("garbage payload should be acked-and-dropped, not nacked; got acked=%v nacked=%v", msg.acked, msg.nacked)
			}
		})
	}
	if got := hdl.snapshot(); len(got) != 0 {
		t.Errorf("handler must not be invoked for invalid data; got %d", len(got))
	}
}
