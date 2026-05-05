package slack

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/hironow/runops-gateway/internal/core/domain"
	"github.com/hironow/runops-gateway/internal/core/port"
)

// DispatchUseCase is the primary port driven by the Slash Command handler.
// Defined locally (not in core/port) because Phase 1 is the only consumer; if
// CLI dispatch lands later it can be promoted to core/port.
type DispatchUseCase interface {
	DispatchAgentTask(ctx context.Context, req domain.DispatchRequest, target port.NotifyTarget) error
}

// CommandHandler handles POST /slack/command (Slash Command Request URL).
type CommandHandler struct {
	useCase       DispatchUseCase
	signingSecret string
}

// NewCommandHandler returns a Slash Command handler.
func NewCommandHandler(useCase DispatchUseCase, signingSecret string) *CommandHandler {
	return &CommandHandler{useCase: useCase, signingSecret: signingSecret}
}

// ServeHTTP implements http.Handler. Slack expects 200 within 3 seconds; the
// dispatch use case runs in a goroutine and the immediate response is either
// an ephemeral error or an empty 200 (followed by response_url updates from the
// use case via the Notifier port).
func (h *CommandHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := VerifySignature(r.Header, body, h.signingSecret); err != nil {
		slog.Warn("slack command: signature verification failed", "error", err)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// io.ReadAll consumed r.Body above; ParseForm needs to read it again.
	r.Body = io.NopCloser(strings.NewReader(string(body)))
	if err := r.ParseForm(); err != nil {
		writeEphemeral(w, "❌ リクエスト形式が不正です")
		return
	}

	cmd := r.PostFormValue("command")
	text := strings.TrimSpace(r.PostFormValue("text"))
	userID := r.PostFormValue("user_id")
	responseURL := r.PostFormValue("response_url")

	if cmd == "" {
		writeEphemeral(w, "❌ command が空です")
		return
	}

	roleStr, freeText := parseSlashCommandText(text)
	role, err := domain.ParseAgentRole(roleStr)
	if err != nil {
		writeEphemeral(w, fmt.Sprintf("❌ %v", err))
		return
	}
	if freeText == "" {
		writeEphemeral(w, "❌ `/agent <role> <task description>` の形式で指示内容を渡してください")
		return
	}
	_ = cmd // command field is validated above; not echoed back to avoid reflecting user input

	req := domain.DispatchRequest{
		Role:           role,
		Text:           freeText,
		RequesterID:    userID,
		IdempotencyKey: newIdempotencyKey(),
		IssuedAt:       time.Now().Unix(),
	}
	target := port.NotifyTarget{CallbackURL: responseURL, Mode: port.ModeSlack}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
		defer cancel()
		if err := h.useCase.DispatchAgentTask(ctx, req, target); err != nil {
			slog.Error("DispatchAgentTask failed", "error", err)
		}
	}()

	w.WriteHeader(http.StatusOK)
}

// parseSlashCommandText splits "<role> <free text>" into the two parts.
// Whitespace-only or empty input yields ("", ""); a role-only input yields
// (role, "") so the handler can render a clearer error.
func parseSlashCommandText(s string) (role, text string) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return "", ""
	}
	parts := strings.SplitN(trimmed, " ", 2)
	role = parts[0]
	if len(parts) == 2 {
		text = strings.TrimSpace(parts[1])
	}
	return role, text
}

// newIdempotencyKey returns a 16-byte hex string. Crypto-random so that two
// independent operators submitting the same /agent text within the same second
// do not collide on OperationKey.
func newIdempotencyKey() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// Extremely unlikely; fall back to time-based key so the dispatch can
		// still proceed (dedup degrades but functionality remains).
		return fmt.Sprintf("t-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// writeEphemeral writes a Slack ephemeral response (visible to the invoker only).
// Used for synchronous validation errors that must not pollute the channel.
func writeEphemeral(w http.ResponseWriter, text string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	resp := map[string]string{
		"response_type": "ephemeral",
		"text":          text,
	}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		slog.Error("ephemeral response encode failed", "err", err)
	}
}
