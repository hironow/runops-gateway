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

	outputslack "github.com/hironow/runops-gateway/internal/adapter/output/slack"
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
//
// F-5 fix (Phase 1 review findings): the handler does NOT execute the dispatch
// directly. It returns an ephemeral Block Kit confirmation that requires the
// operator to click Approve before DispatchAgentTask runs. The Approve click
// arrives at /slack/interactive and is dispatched by InteractiveHandler.
type CommandHandler struct {
	signingSecret string
}

// NewCommandHandler returns a Slash Command handler.
func NewCommandHandler(signingSecret string) *CommandHandler {
	return &CommandHandler{signingSecret: signingSecret}
}

// ServeHTTP implements http.Handler. Slack expects 200 within 3 seconds; the
// confirmation Block Kit is returned synchronously in the response body
// (response_type: ephemeral). DispatchAgentTask only runs after the operator
// clicks Approve via /slack/interactive.
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

	if cmd == "" {
		writeEphemeral(w, "❌ command が空です")
		return
	}

	roleStr, projectID, freeText, err := parseSlashCommandText(text)
	if err != nil {
		writeEphemeral(w, fmt.Sprintf("❌ %v", err))
		return
	}
	role, err := domain.ParseAgentRole(roleStr)
	if err != nil {
		writeEphemeral(w, fmt.Sprintf("❌ %v", err))
		return
	}
	if freeText == "" {
		writeEphemeral(w, "❌ `/agent <role> [--project=<id>] <task description>` の形式で指示内容を渡してください")
		return
	}
	_ = cmd // command field is validated above; not echoed back to avoid reflecting user input

	idempotencyKey := newIdempotencyKey()
	issuedAt := time.Now().Unix()

	approveValue, denyValue, err := buildDispatchButtonValues(role, freeText, userID, idempotencyKey, projectID, issuedAt)
	if err != nil {
		slog.Error("failed to build dispatch button values", "err", err)
		writeEphemeral(w, "❌ 確認メッセージの組み立てに失敗しました")
		return
	}

	confirmation := outputslack.BuildDispatchConfirmation(outputslack.DispatchConfirmation{
		Role:           string(role),
		Text:           freeText,
		RequesterID:    userID,
		IdempotencyKey: idempotencyKey,
		ApproveValue:   approveValue,
		DenyValue:      denyValue,
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(confirmation); err != nil {
		slog.Error("failed to encode confirmation payload", "err", err)
	}
}

// buildDispatchButtonValues returns the compressed payloads to embed in the
// Approve and Deny buttons of the dispatch confirmation. Both carry the same
// dispatchActionValue so InteractiveHandler can rebuild a DispatchRequest
// regardless of which button is clicked.
func buildDispatchButtonValues(role domain.AgentRole, text, requesterID, idempotencyKey, projectID string, issuedAt int64) (approve, deny string, err error) {
	dv := dispatchActionValue{
		Role:           string(role),
		Text:           text,
		RequesterID:    requesterID,
		IdempotencyKey: idempotencyKey,
		IssuedAt:       issuedAt,
		ProjectID:      projectID,
	}
	raw, err := marshalDispatchActionValue(dv)
	if err != nil {
		return "", "", fmt.Errorf("marshal dispatch action value: %w", err)
	}
	encoded := outputslack.CompressButtonValue(string(raw))
	return encoded, encoded, nil
}

// parseSlashCommandText is a reject-first parser for `/agent <role> [--project=<id> | --project <id>] <text>`.
//
// Allowed grammars (only these three):
//  1. "<role>"                                 — role-only, text="", project=""
//  2. "<role> <text>"                          — backward compat, project=""
//  3. "<role> --project=<id> <text>"           — flag = form
//  4. "<role> --project <id> <text>"           — flag space form
//
// Anything else (multiple --project, empty value, role-only with --project,
// invalid project_id format) is rejected with an error so the caller can
// surface a Slack ephemeral message. The flag is only recognized as the
// SECOND token after role; "--project" appearing inside the free text is
// passed through as a literal string.
func parseSlashCommandText(s string) (role, projectID, text string, err error) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return "", "", "", nil
	}
	parts := strings.SplitN(trimmed, " ", 2)
	role = parts[0]
	if len(parts) == 1 {
		// role-only is allowed; caller decides whether empty text is an error.
		return role, "", "", nil
	}
	rest := strings.TrimSpace(parts[1])

	headParts := strings.SplitN(rest, " ", 2)
	head := headParts[0]
	tail := ""
	if len(headParts) == 2 {
		tail = strings.TrimSpace(headParts[1])
	}

	switch {
	case strings.HasPrefix(head, "--project="):
		value := strings.TrimPrefix(head, "--project=")
		if value == "" {
			return "", "", "", fmt.Errorf("--project= requires a value")
		}
		if err := domain.ValidateProjectID(value); err != nil {
			return "", "", "", fmt.Errorf("invalid --project value: %w", err)
		}
		if tail == "" {
			return "", "", "", fmt.Errorf("--project=<id> must be followed by the request text")
		}
		if containsProjectFlag(tail) {
			return "", "", "", fmt.Errorf("multiple --project flags not allowed")
		}
		return role, value, tail, nil
	case head == "--project":
		if tail == "" {
			return "", "", "", fmt.Errorf("--project must be followed by a value and the request text")
		}
		valueParts := strings.SplitN(tail, " ", 2)
		value := valueParts[0]
		if err := domain.ValidateProjectID(value); err != nil {
			return "", "", "", fmt.Errorf("invalid --project value: %w", err)
		}
		if len(valueParts) < 2 || strings.TrimSpace(valueParts[1]) == "" {
			return "", "", "", fmt.Errorf("--project <id> must be followed by the request text")
		}
		remainder := strings.TrimSpace(valueParts[1])
		if containsProjectFlag(remainder) {
			return "", "", "", fmt.Errorf("multiple --project flags not allowed")
		}
		return role, value, remainder, nil
	default:
		// No flag at the head position — entire rest is free text. Any
		// "--project" inside is literal and passed through unchanged.
		return role, "", rest, nil
	}
}

// containsProjectFlag reports whether s contains a `--project` flag at the
// start of any whitespace-delimited token. Used to reject duplicate flag
// usage after the head has already consumed one.
func containsProjectFlag(s string) bool {
	for _, tok := range strings.Fields(s) {
		if tok == "--project" || strings.HasPrefix(tok, "--project=") {
			return true
		}
	}
	return false
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
