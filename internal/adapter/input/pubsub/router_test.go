package pubsub

import (
	"context"
	"errors"
	"testing"
)

// staticMessage minimally satisfies Message for router unit tests; only
// Attributes is read so the rest can panic loud if a future change to
// the router taps unexpected message internals.
type staticMessage struct {
	attrs map[string]string
}

func (m *staticMessage) ID() string                    { return "static" }
func (m *staticMessage) Data() []byte                  { return nil }
func (m *staticMessage) Attributes() map[string]string { return m.attrs }
func (m *staticMessage) Ack()                          {}
func (m *staticMessage) Nack()                         {}

func TestSingleOutboxRouter_IgnoresProjectID(t *testing.T) {
	w := &fakeWriter{}
	r := NewSingleOutboxRouter(w)

	for _, attrs := range []map[string]string{
		nil,
		{},
		{"project_id": "foo"},
		{"project_id": "bar"},
		{"project_id": ""},
		{"project_id": "this-id-does-not-exist"},
	} {
		got, err := r.Resolve(context.Background(), &staticMessage{attrs: attrs})
		if err != nil {
			t.Fatalf("SingleOutboxRouter.Resolve(attrs=%v) returned error: %v", attrs, err)
		}
		if got != Writer(w) {
			t.Errorf("SingleOutboxRouter.Resolve(attrs=%v) writer mismatch", attrs)
		}
	}
}

func TestMultiOutboxRouter_RoutesByProjectID(t *testing.T) {
	wFoo := &fakeWriter{}
	wBar := &fakeWriter{}
	r := NewMultiOutboxRouter(map[string]Writer{
		"foo": wFoo,
		"bar": wBar,
	})

	cases := []struct {
		name    string
		project string
		want    *fakeWriter
	}{
		{"foo", "foo", wFoo},
		{"bar", "bar", wBar},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := r.Resolve(context.Background(), &staticMessage{
				attrs: map[string]string{"project_id": tc.project},
			})
			if err != nil {
				t.Fatalf("Resolve(%q): %v", tc.project, err)
			}
			if got != Writer(tc.want) {
				t.Errorf("Resolve(%q) returned wrong writer", tc.project)
			}
		})
	}
}

func TestMultiOutboxRouter_RejectsUnmappedProjectID(t *testing.T) {
	r := NewMultiOutboxRouter(map[string]Writer{
		"foo": &fakeWriter{},
	})

	_, err := r.Resolve(context.Background(), &staticMessage{
		attrs: map[string]string{"project_id": "ghost"},
	})
	if !errors.Is(err, ErrProjectNotRouted) {
		t.Fatalf("want ErrProjectNotRouted, got %v", err)
	}
}

func TestMultiOutboxRouter_RejectsMissingProjectID(t *testing.T) {
	r := NewMultiOutboxRouter(map[string]Writer{
		"foo": &fakeWriter{},
	})

	for _, attrs := range []map[string]string{
		nil,
		{},
		{"project_id": ""},
	} {
		_, err := r.Resolve(context.Background(), &staticMessage{attrs: attrs})
		if !errors.Is(err, ErrProjectNotRouted) {
			t.Errorf("attrs=%v: want ErrProjectNotRouted, got %v", attrs, err)
		}
	}
}

func TestMultiOutboxRouter_RejectsInvalidProjectID(t *testing.T) {
	r := NewMultiOutboxRouter(map[string]Writer{
		"foo": &fakeWriter{},
	})

	for _, bad := range []string{
		"with space",
		"slash/in/id",
		"dot.in.id",
		"upper@lower",
	} {
		_, err := r.Resolve(context.Background(), &staticMessage{
			attrs: map[string]string{"project_id": bad},
		})
		if !errors.Is(err, ErrProjectNotRouted) {
			t.Errorf("project_id=%q: want ErrProjectNotRouted, got %v", bad, err)
		}
	}
}
