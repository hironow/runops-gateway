package dispatcher

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/hironow/runops-gateway/internal/core/domain"
	"github.com/hironow/runops-gateway/internal/core/port"
)

// compile-time interface assertion
var _ port.Dispatcher = (*StubDispatcher)(nil)

func TestStubDispatcher_LogsRequestFields(t *testing.T) {
	// given
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	d := NewStubDispatcher(logger)
	req := domain.DispatchRequest{
		Role:           domain.AgentRolePaintress,
		Text:           "fix M-42",
		RequesterID:    "U0123ABCD",
		IdempotencyKey: "k-001",
		IssuedAt:       1700000000,
	}

	// when
	if err := d.Dispatch(context.Background(), req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// then
	out := buf.String()
	for _, want := range []string{"dispatched stub", "paintress", "fix M-42", "U0123ABCD", "k-001"} {
		if !strings.Contains(out, want) {
			t.Errorf("log should contain %q; got:\n%s", want, out)
		}
	}
}

func TestStubDispatcher_NilLoggerFallsBackToDefault(t *testing.T) {
	// given — passing nil logger must not panic; falls back to slog.Default()
	d := NewStubDispatcher(nil)
	req := domain.DispatchRequest{
		Role:        domain.AgentRoleSightjack,
		Text:        "scan",
		RequesterID: "U0001",
	}

	// when / then
	if err := d.Dispatch(context.Background(), req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStubDispatcher_RejectsZeroRole(t *testing.T) {
	// given — defensive check: caller must set a role
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	d := NewStubDispatcher(logger)

	// when
	err := d.Dispatch(context.Background(), domain.DispatchRequest{
		RequesterID: "U0001",
		Text:        "x",
	})

	// then
	if err == nil {
		t.Fatal("expected error for empty Role, got nil")
	}
}
