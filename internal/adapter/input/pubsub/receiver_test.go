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

func (m *fakeMessage) ID() string                  { return m.id }
func (m *fakeMessage) Data() []byte                { return m.data }
func (m *fakeMessage) Attributes() map[string]string { return m.attributes }
func (m *fakeMessage) Ack()                        { m.acked = true }
func (m *fakeMessage) Nack()                       { m.nacked = true }

func TestReceiver_OnMessage_WritesUsingIDAttributeAsFilename(t *testing.T) {
	w := &fakeWriter{}
	r := NewReceiver(w)

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
	r := NewReceiver(w)
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
	r := NewReceiver(w)
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
	r := NewReceiver(w)
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
	r := NewReceiver(w)
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
