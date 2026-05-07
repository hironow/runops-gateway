package pubsub

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

// fakeWriter records every WriteFile call and returns a configurable error.
type fakeWriter struct {
	mu     sync.Mutex
	writes []writeRecord
	err    error
}

type writeRecord struct {
	Name string
	Data []byte
}

func (w *fakeWriter) WriteFile(name string, data []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.writes = append(w.writes, writeRecord{Name: name, Data: append([]byte(nil), data...)})
	return w.err
}

func (w *fakeWriter) snapshot() []writeRecord {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]writeRecord, len(w.writes))
	copy(out, w.writes)
	return out
}

// fakeMessage adapts to the receiver's expected message interface so the
// tests do not depend on the Cloud SDK *pubsub.Message type.
type fakeMessage struct {
	id         string
	data       []byte
	attributes map[string]string
	acked      bool
	nacked     bool
}

func (m *fakeMessage) ID() string                    { return m.id }
func (m *fakeMessage) Data() []byte                  { return m.data }
func (m *fakeMessage) Attributes() map[string]string { return m.attributes }
func (m *fakeMessage) Ack()                          { m.acked = true }
func (m *fakeMessage) Nack()                         { m.nacked = true }

func TestReceiver_OnMessage_WritesUsingIDAttributeAsFilename(t *testing.T) {
	w := &fakeWriter{}
	r := NewReceiver(NewSingleOutboxRouter(w))

	msg := &fakeMessage{
		id:   "publisher-id-1",
		data: []byte("---\nkind: specification\n---\n\nbody\n"),
		attributes: map[string]string{
			"id":          "01HZW0K0AB12CD34EF56GH78JK",
			"kind":        "specification",
			"target_tool": "paintress",
		},
	}

	r.OnMessage(context.Background(), msg)

	got := w.snapshot()
	if len(got) != 1 {
		t.Fatalf("expected one write, got %d", len(got))
	}
	if got[0].Name != "01HZW0K0AB12CD34EF56GH78JK.md" {
		t.Errorf("filename should derive from id attribute; got %q", got[0].Name)
	}
	if !msg.acked {
		t.Error("message should be acked after successful write")
	}
}

func TestReceiver_OnMessage_FallsBackToPubsubIDIfNoIDAttribute(t *testing.T) {
	w := &fakeWriter{}
	r := NewReceiver(NewSingleOutboxRouter(w))
	msg := &fakeMessage{
		id:         "fallback-id",
		data:       []byte("body"),
		attributes: map[string]string{"kind": "report", "target_tool": "amadeus"},
	}
	r.OnMessage(context.Background(), msg)
	got := w.snapshot()
	if len(got) != 1 {
		t.Fatalf("expected one write, got %d", len(got))
	}
	if got[0].Name != "fallback-id.md" {
		t.Errorf("filename should fall back to pubsub message id; got %q", got[0].Name)
	}
}

func TestReceiver_OnMessage_NacksOnWriterError(t *testing.T) {
	w := &fakeWriter{err: errors.New("disk full")}
	r := NewReceiver(NewSingleOutboxRouter(w))
	msg := &fakeMessage{
		id:         "msg-1",
		data:       []byte("body"),
		attributes: map[string]string{"kind": "report"},
	}
	r.OnMessage(context.Background(), msg)
	if !msg.nacked {
		t.Error("message should be nacked when writer fails (Pub/Sub will retry)")
	}
	if msg.acked {
		t.Error("message must not be acked when writer fails")
	}
}

func TestReceiver_OnMessage_AcksAndDropsMessagesWithEmptyData(t *testing.T) {
	w := &fakeWriter{}
	r := NewReceiver(NewSingleOutboxRouter(w))
	msg := &fakeMessage{
		id:         "empty-1",
		data:       nil,
		attributes: map[string]string{"kind": "report"},
	}
	r.OnMessage(context.Background(), msg)
	if !msg.acked {
		t.Error("empty message should be acked (dropped, not retried)")
	}
	if got := w.snapshot(); len(got) != 0 {
		t.Errorf("writer must not be called for empty data; got %d writes", len(got))
	}
}

func TestReceiver_OnMessage_RejectsUnsafeIDAttribute(t *testing.T) {
	// Attribute-controlled filename → must be sanitized.
	w := &fakeWriter{}
	r := NewReceiver(NewSingleOutboxRouter(w))
	msg := &fakeMessage{
		id:   "msg-bad",
		data: []byte("body"),
		attributes: map[string]string{
			"id":   "../escape", // path traversal attempt
			"kind": "report",
		},
	}
	r.OnMessage(context.Background(), msg)
	if !msg.acked {
		t.Error("message with invalid id should still be acked (no point retrying)")
	}
	got := w.snapshot()
	for _, g := range got {
		if strings.Contains(g.Name, "..") {
			t.Errorf("filename must not include parent traversal; got %q", g.Name)
		}
	}
}

func TestReceiver_OnMessage_NacksOnProjectNotRouted(t *testing.T) {
	// Multi-mode router that knows only "foo"; an incoming message with
	// project_id=ghost must be nacked so Pub/Sub max_delivery_attempts
	// pushes it to the DLQ for operator triage (#0006 / ADR 0028).
	w := &fakeWriter{}
	r := NewReceiver(NewMultiOutboxRouter(map[string]Writer{
		"foo": w,
	}))
	msg := &fakeMessage{
		id:   "msg-multi-1",
		data: []byte("body"),
		attributes: map[string]string{
			"id":         "01HZW0K0AB12CD34EF56GH78JK",
			"project_id": "ghost",
		},
	}

	r.OnMessage(context.Background(), msg)

	if !msg.nacked {
		t.Errorf("unrouted project_id should nack the message")
	}
	if msg.acked {
		t.Errorf("unrouted project_id must NOT ack the message")
	}
	if got := w.snapshot(); len(got) != 0 {
		t.Errorf("writer for 'foo' should not see ghost message; got %d writes", len(got))
	}
}

func TestReceiver_OnMessage_RoutesByProjectIDInMultiMode(t *testing.T) {
	// Multi-mode happy path: project_id=bar lands in bar's writer, not
	// foo's, and the message is acked.
	wFoo := &fakeWriter{}
	wBar := &fakeWriter{}
	r := NewReceiver(NewMultiOutboxRouter(map[string]Writer{
		"foo": wFoo,
		"bar": wBar,
	}))
	msg := &fakeMessage{
		id:   "msg-multi-2",
		data: []byte("payload"),
		attributes: map[string]string{
			"id":         "01HZW0K0AB12CD34EF56GH78JK",
			"project_id": "bar",
		},
	}

	r.OnMessage(context.Background(), msg)

	if got := wBar.snapshot(); len(got) != 1 {
		t.Errorf("bar writer should see exactly one write, got %d", len(got))
	}
	if got := wFoo.snapshot(); len(got) != 0 {
		t.Errorf("foo writer should see no writes, got %d", len(got))
	}
	if !msg.acked {
		t.Errorf("multi-mode happy path should ack the message")
	}
}

func TestReceiver_OnMessage_SingleModeIgnoresProjectID(t *testing.T) {
	// Backward-compat assertion: even when a publisher upstream sets
	// project_id, single-mode receivers must hand the message to the
	// sole writer without consulting the attribute. Confirms the
	// SingleOutboxRouter contract end-to-end through Receiver.
	w := &fakeWriter{}
	r := NewReceiver(NewSingleOutboxRouter(w))
	msg := &fakeMessage{
		id:   "msg-single-1",
		data: []byte("payload"),
		attributes: map[string]string{
			"id":         "01HZW0K0AB12CD34EF56GH78JK",
			"project_id": "any-value-ignored",
		},
	}

	r.OnMessage(context.Background(), msg)

	if got := w.snapshot(); len(got) != 1 {
		t.Errorf("single-mode writer should see exactly one write, got %d", len(got))
	}
	if !msg.acked {
		t.Errorf("single-mode happy path should ack the message")
	}
}

// silence unused strings import if subsequent commits drop it.
var _ = strings.Builder{}
