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

func TestStubDispatcher_LogsSafeMetadataOnly(t *testing.T) {
	// given
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	d := NewStubDispatcher(logger)
	req := domain.DispatchRequest{
		Role:           domain.AgentRolePaintress,
		Text:           "fix M-42 token=AKIA-secret",
		RequesterID:    "U0123ABCD",
		IdempotencyKey: "k-001",
		IssuedAt:       1700000000,
	}

	// when
	if err := d.Dispatch(context.Background(), req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// then — must contain non-sensitive metadata
	out := buf.String()
	for _, want := range []string{"dispatched stub", "paintress", "U0123ABCD", "k-001", "text_len=26"} {
		if !strings.Contains(out, want) {
			t.Errorf("log should contain %q; got:\n%s", want, out)
		}
	}
	// and must contain a fingerprint of the text (8-char SHA-256 prefix)
	if !strings.Contains(out, "text_sha256=") {
		t.Errorf("log should contain text_sha256= prefix; got:\n%s", out)
	}
}

func TestStubDispatcher_DoesNotEmitTextLiteralOrSecret(t *testing.T) {
	// given — text contains both a literal phrase and a fake secret
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	d := NewStubDispatcher(logger)
	req := domain.DispatchRequest{
		Role:        domain.AgentRolePaintress,
		Text:        "fix M-42 token=AKIA-secret",
		RequesterID: "U0123",
	}

	// when
	if err := d.Dispatch(context.Background(), req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// then — neither the literal text nor the embedded secret may appear in the log
	out := buf.String()
	for _, mustNotAppear := range []string{"fix M-42", "AKIA-secret", "token=AKIA"} {
		if strings.Contains(out, mustNotAppear) {
			t.Errorf("log MUST NOT contain raw text fragment %q; got:\n%s", mustNotAppear, out)
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
