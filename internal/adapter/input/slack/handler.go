package slack

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
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

// actionValue is the JSON embedded in Slack button's value field.
// Plural fields (ResourceNames, Targets, NextServiceNames, NextRevisions) are the
// current canonical form. Singular fields are retained for backward compatibility
// with legacy button payloads already posted in Slack.
type actionValue struct {
	Project          string `json:"project"`
	Location         string `json:"location"`
	ResourceType     string `json:"resource_type"`
	ResourceNames    string `json:"resource_names"`
	ResourceName     string `json:"resource_name"` // legacy: singular form
	Targets          string `json:"targets"`
	Target           string `json:"target"` // legacy: singular form
	Action           string `json:"action"`
	IssuedAt         int64  `json:"issued_at"`
	MigrationDone    bool   `json:"migration_done"`
	NextServiceNames string `json:"next_service_names"`
	NextServiceName  string `json:"next_service_name"` // legacy: singular form
	NextRevisions    string `json:"next_revisions"`
	NextRevision     string `json:"next_revision"` // legacy: singular form
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
	av, err := parseActionValue(action.Value)
	if err != nil {
		slog.Warn("failed to parse action value", "error", err)
		w.WriteHeader(http.StatusOK)
		return
	}
	if av.Project == "" || av.Location == "" {
		slog.Warn("missing project or location in button value", "project", av.Project, "location", av.Location)
		w.WriteHeader(http.StatusOK)
		return
	}
	req := domain.ApprovalRequest{
		Project:          av.Project,
		Location:         av.Location,
		ResourceType:     domain.ResourceType(av.ResourceType),
		ResourceNames:    firstNonEmpty(av.ResourceNames, av.ResourceName),
		Targets:          firstNonEmpty(av.Targets, av.Target),
		Action:           av.Action,
		ApproverID:       slackPayload.User.ID,
		IssuedAt:         av.IssuedAt,
		MigrationDone:    av.MigrationDone,
		NextServiceNames: firstNonEmpty(av.NextServiceNames, av.NextServiceName),
		NextRevisions:    firstNonEmpty(av.NextRevisions, av.NextRevision),
		NextAction:       av.NextAction,
	}
	target := port.NotifyTarget{
		CallbackURL: slackPayload.ResponseURL,
		Mode:        port.ModeSlack,
	}
	// 4. Dispatch asynchronously (avoid Slack 3-second timeout).
	// Use a 25-minute timeout to stay within Slack's 30-minute response_url validity,
	// leaving 5 minutes of margin for the final notification POST.
	const responseURLTimeout = 25 * time.Minute
	switch {
	case strings.HasPrefix(action.ActionID, "approve"):
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), responseURLTimeout)
			defer cancel()
			if err := h.useCase.ApproveAction(ctx, req, target); err != nil {
				slog.Error("ApproveAction failed", "error", err)
			}
		}()
	case action.ActionID == "deny":
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), responseURLTimeout)
			defer cancel()
			if err := h.useCase.DenyAction(ctx, req, target); err != nil {
				slog.Error("DenyAction failed", "error", err)
			}
		}()
	default:
		slog.Warn("unknown action_id", "action_id", action.ActionID)
	}
	// 5. Immediately return 200 OK
	w.WriteHeader(http.StatusOK)
}

// parseActionValue parses a Slack button value into an actionValue.
// Values with the "gz:" prefix are decompressed (gzip + base64url) before JSON parsing.
// This is the counterpart of compressButtonValue in adapter/output/slack/blockkit.go.
func parseActionValue(s string) (actionValue, error) {
	var av actionValue
	data := []byte(s)
	if strings.HasPrefix(s, "gz:") {
		decoded, err := base64.RawURLEncoding.DecodeString(s[3:])
		if err != nil {
			return av, fmt.Errorf("base64 decode button value: %w", err)
		}
		r, err := gzip.NewReader(bytes.NewReader(decoded))
		if err != nil {
			return av, fmt.Errorf("gzip reader for button value: %w", err)
		}
		defer r.Close()
		expanded, err := io.ReadAll(r)
		if err != nil {
			return av, fmt.Errorf("decompress button value: %w", err)
		}
		data = expanded
	}
	if err := json.Unmarshal(data, &av); err != nil {
		return av, err
	}
	return av, nil
}

// firstNonEmpty returns a if non-empty, otherwise b.
// Used to prefer plural (new) fields while falling back to singular (legacy) fields.
func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
