package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"

	"github.com/hironow/runops-gateway/internal/core/domain"
	"github.com/hironow/runops-gateway/internal/core/port"
)

// actionValue is the JSON embedded in Slack button's value field.
// Plural fields (ResourceNames, Targets, NextServiceNames, NextRevisions) are the
// current canonical form. Singular fields are retained for backward compatibility
// with legacy button payloads already posted in Slack.
type actionValue struct {
	ResourceType     string `json:"resource_type"`
	ResourceNames    string `json:"resource_names"`
	ResourceName     string `json:"resource_name"`     // legacy: singular form
	Targets          string `json:"targets"`
	Target           string `json:"target"`            // legacy: singular form
	Action           string `json:"action"`
	IssuedAt         int64  `json:"issued_at"`
	MigrationDone    bool   `json:"migration_done"`
	NextServiceNames string `json:"next_service_names"`
	NextServiceName  string `json:"next_service_name"` // legacy: singular form
	NextRevisions    string `json:"next_revisions"`
	NextRevision     string `json:"next_revision"`     // legacy: singular form
	NextAction       string `json:"next_action"`
}

// interactivePayload is a minimal representation of Slack's interactive payload.
type interactivePayload struct {
	User struct {
		ID string `json:"id"`
	} `json:"user"`
	ResponseURL string `json:"response_url"`
	Actions     []struct {
		ActionID string `json:"action_id"`
		Value    string `json:"value"`
	} `json:"actions"`
}

// Handler handles POST /slack/interactive requests.
type Handler struct {
	useCase       port.RunOpsUseCase
	signingSecret string
}

// NewHandler creates a new Slack interactive handler.
func NewHandler(useCase port.RunOpsUseCase, signingSecret string) *Handler {
	return &Handler{useCase: useCase, signingSecret: signingSecret}
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// 1. Read body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	// 2. Verify Slack signature
	if err := VerifySignature(r.Header, body, h.signingSecret); err != nil {
		slog.Warn("slack signature verification failed", "error", err)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	// 3. Parse form payload
	r.Body = io.NopCloser(bytes.NewBuffer(body))
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	payloadJSON := r.FormValue("payload")
	if payloadJSON == "" {
		w.WriteHeader(http.StatusOK)
		return
	}
	var slackPayload interactivePayload
	if err := json.Unmarshal([]byte(payloadJSON), &slackPayload); err != nil {
		slog.Warn("failed to parse slack payload", "error", err)
		w.WriteHeader(http.StatusOK) // Don't 400 — return 200 to Slack to avoid retries
		return
	}
	if len(slackPayload.Actions) == 0 {
		w.WriteHeader(http.StatusOK)
		return
	}
	action := slackPayload.Actions[0]
	var av actionValue
	if err := json.Unmarshal([]byte(action.Value), &av); err != nil {
		slog.Warn("failed to parse action value", "error", err)
		w.WriteHeader(http.StatusOK)
		return
	}
	req := domain.ApprovalRequest{
		ResourceType:     domain.ResourceType(av.ResourceType),
		ResourceNames:    firstNonEmpty(av.ResourceNames, av.ResourceName),
		Targets:          firstNonEmpty(av.Targets, av.Target),
		Action:           av.Action,
		ApproverID:       slackPayload.User.ID,
		Source:           "slack",
		IssuedAt:         av.IssuedAt,
		ResponseURL:      slackPayload.ResponseURL,
		MigrationDone:    av.MigrationDone,
		NextServiceNames: firstNonEmpty(av.NextServiceNames, av.NextServiceName),
		NextRevisions:    firstNonEmpty(av.NextRevisions, av.NextRevision),
		NextAction:       av.NextAction,
	}
	// 4. Dispatch asynchronously (avoid Slack 3-second timeout)
	switch action.ActionID {
	case "approve":
		go func() {
			if err := h.useCase.ApproveAction(context.Background(), req); err != nil {
				slog.Error("ApproveAction failed", "error", err)
			}
		}()
	case "deny":
		go func() {
			if err := h.useCase.DenyAction(context.Background(), req); err != nil {
				slog.Error("DenyAction failed", "error", err)
			}
		}()
	default:
		slog.Warn("unknown action_id", "action_id", action.ActionID)
	}
	// 5. Immediately return 200 OK
	w.WriteHeader(http.StatusOK)
}

// firstNonEmpty returns a if non-empty, otherwise b.
// Used to prefer plural (new) fields while falling back to singular (legacy) fields.
func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
