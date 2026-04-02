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
type actionValue struct {
	ResourceType string `json:"resource_type"`
	ResourceName string `json:"resource_name"`
	Target       string `json:"target"`
	Action       string `json:"action"`
	IssuedAt     int64  `json:"issued_at"`
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
		ResourceType: domain.ResourceType(av.ResourceType),
		ResourceName: av.ResourceName,
		Target:       av.Target,
		Action:       av.Action,
		ApproverID:   slackPayload.User.ID,
		Source:       "slack",
		IssuedAt:     av.IssuedAt,
		ResponseURL:  slackPayload.ResponseURL,
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
