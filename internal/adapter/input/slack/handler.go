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
	BuildInfo        string `json:"build_info"`
}

// interactiveAction is a single Block Kit action element from a Slack
// interactive payload (button click, etc.).
type interactiveAction struct {
	ActionID string `json:"action_id"`
	Value    string `json:"value"`
}

// interactivePayload is a minimal representation of Slack's interactive payload.
type interactivePayload struct {
	User struct {
		ID string `json:"id"`
	} `json:"user"`
	Channel struct {
		ID string `json:"id"`
	} `json:"channel"`
	Message struct {
		TS string `json:"ts"`
	} `json:"message"`
	ResponseURL string              `json:"response_url"`
	Actions     []interactiveAction `json:"actions"`
}

// InteractiveHandler handles POST /slack/interactive requests
// (block-kit button clicks: approve / deny / dispatch_approve / dispatch_deny).
type InteractiveHandler struct {
	useCase           port.RunOpsUseCase
	dispatchUseCase   DispatchUseCase
	notifier          port.Notifier
	consumedTokens    port.ConsumedTokenStore
	signingSecret     string
}

// NewInteractiveHandler creates a new Slack interactive (button click) handler.
// dispatchUseCase may be nil when the deployment does not enable Phase 1
// /agent dispatch; dispatch_* actions then no-op. consumedTokens defends the
// dispatch_approve path against button replay (Codex round 4 finding 2);
// when nil the guard is disabled — callers must wire one in production.
func NewInteractiveHandler(useCase port.RunOpsUseCase, dispatchUseCase DispatchUseCase, notifier port.Notifier, consumedTokens port.ConsumedTokenStore, signingSecret string) *InteractiveHandler {
	return &InteractiveHandler{
		useCase:         useCase,
		dispatchUseCase: dispatchUseCase,
		notifier:        notifier,
		consumedTokens:  consumedTokens,
		signingSecret:   signingSecret,
	}
}

// responseURLTimeout matches Slack's 30-minute response_url validity, leaving
// 5 minutes of margin for the final notification POST. Used by every async
// goroutine in ServeHTTP.
const responseURLTimeout = 25 * time.Minute

// ServeHTTP implements http.Handler.
func (h *InteractiveHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
	target := port.NotifyTarget{
		CallbackURL: slackPayload.ResponseURL,
		Mode:        port.ModeSlack,
		// ChannelID + ThreadTS let FallbackNotifier (ADR 0017) drop into
		// chat.postMessage when the response_url has expired or hit its 5-call
		// limit. Both are optional from Slack's side and stay empty for any
		// interaction that did not originate from a Block Kit message.
		ChannelID: slackPayload.Channel.ID,
		ThreadTS:  slackPayload.Message.TS,
	}

	// dispatch_* actions carry a dispatchActionValue payload (Phase 1 / F-5 fix),
	// not the actionValue used by approve/deny. Branch early so we do not run
	// the actionValue parser on the wrong shape.
	if action.ActionID == "dispatch_approve" || action.ActionID == "dispatch_deny" {
		h.handleDispatchAction(action, slackPayload.User.ID, target)
		w.WriteHeader(http.StatusOK)
		return
	}

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
		BuildInfo:        av.BuildInfo,
	}
	// 4. Dispatch asynchronously (avoid Slack 3-second timeout).
	switch {
	case strings.HasPrefix(action.ActionID, "approve"):
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), responseURLTimeout)
			defer cancel()
			if err := h.useCase.ApproveAction(ctx, req, target); err != nil {
				slog.Error("ApproveAction failed", "error", err)
				h.notifyIfTimeout(ctx, err, target)
			}
		}()
	case action.ActionID == "deny":
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), responseURLTimeout)
			defer cancel()
			if err := h.useCase.DenyAction(ctx, req, target); err != nil {
				slog.Error("DenyAction failed", "error", err)
				h.notifyIfTimeout(ctx, err, target)
			}
		}()
	default:
		slog.Warn("unknown action_id", "action_id", action.ActionID)
	}
	// 5. Immediately return 200 OK
	w.WriteHeader(http.StatusOK)
}

// handleDispatchAction routes the two Slash Command confirmation buttons.
// dispatch_approve runs the use case asynchronously; dispatch_deny replaces the
// confirmation message with a cancellation note and never invokes the use case.
//
// Both buttons require the clicker to be the original requester (Phase 1
// hijack guard, Codex Review round 4 finding 1). Phase 4 will lift this for
// HIGH severity 4-eyes flows.
func (h *InteractiveHandler) handleDispatchAction(action interactiveAction, clickerUserID string, target port.NotifyTarget) {
	dv, err := parseDispatchActionValue(action.Value)
	if err != nil {
		slog.Warn("failed to parse dispatch action value", "error", err)
		return
	}
	if dv.Role == "" {
		slog.Warn("dispatch action value missing role")
		return
	}

	// Hijack guard: never trust the requester ID embedded in the payload as the
	// click-time approver. Cross-check the clicker's Slack user ID instead.
	if clickerUserID == "" || clickerUserID != dv.RequesterID {
		slog.Warn("dispatch action clicker mismatch",
			"action", action.ActionID, "clicker", clickerUserID, "requester", dv.RequesterID)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := h.notifier.SendEphemeral(ctx, target, clickerUserID,
				"🚫 自分が発行した dispatch のみ承認・キャンセルできます"); err != nil {
				slog.Error("dispatch hijack ephemeral notification failed", "error", err)
			}
		}()
		return
	}

	if action.ActionID == "dispatch_deny" {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), responseURLTimeout)
			defer cancel()
			if err := h.notifier.UpdateMessage(ctx, target, "🚫 Dispatch をキャンセルしました"); err != nil {
				slog.Error("dispatch_deny notification failed", "error", err)
			}
		}()
		return
	}

	// dispatch_approve
	if h.dispatchUseCase == nil {
		slog.Warn("dispatch_approve received but DispatchUseCase is not wired")
		return
	}

	// One-time consume guard: a single confirmation button must run the use
	// case at most once even if Slack retries the click or a network replay
	// re-fires the same payload (Codex round 4 finding 2).
	if h.consumedTokens != nil {
		token := dispatchApproveToken(dv)
		if !h.consumedTokens.MarkConsumed(token) {
			slog.Warn("dispatch_approve replay rejected", "token", token, "clicker", clickerUserID)
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				if err := h.notifier.SendEphemeral(ctx, target, clickerUserID,
					"⚠️ この dispatch は既に処理済みです（重複クリック防止）"); err != nil {
					slog.Error("dispatch replay ephemeral failed", "error", err)
				}
			}()
			return
		}
	}

	req := domain.DispatchRequest{
		Role:           domain.AgentRole(dv.Role),
		Text:           dv.Text,
		RequesterID:    clickerUserID, // trust the clicker, not the payload
		IdempotencyKey: dv.IdempotencyKey,
		IssuedAt:       dv.IssuedAt,
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), responseURLTimeout)
		defer cancel()
		if err := h.dispatchUseCase.DispatchAgentTask(ctx, req, target); err != nil {
			slog.Error("DispatchAgentTask failed", "error", err)
		}
	}()
}

// dispatchApproveToken derives the consumed-token key for a dispatchActionValue.
// Includes IdempotencyKey + IssuedAt + RequesterID so two distinct /agent
// invocations from the same operator produce distinct tokens; replay of the
// exact same button payload always collides on the same key.
func dispatchApproveToken(dv dispatchActionValue) string {
	return fmt.Sprintf("dispatch_approve/%s/%s/%d",
		dv.RequesterID, dv.IdempotencyKey, dv.IssuedAt)
}

// decodeButtonValue undoes the encoding applied by compressButtonValue. Values
// with the "gz:" prefix are gzip + base64url decoded; legacy raw JSON values
// pass through. Shared by parseActionValue and parseDispatchActionValue so the
// two payload shapes use exactly the same transport.
func decodeButtonValue(s string) ([]byte, error) {
	if !strings.HasPrefix(s, "gz:") {
		return []byte(s), nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(s[3:])
	if err != nil {
		return nil, fmt.Errorf("base64 decode button value: %w", err)
	}
	r, err := gzip.NewReader(bytes.NewReader(decoded))
	if err != nil {
		return nil, fmt.Errorf("gzip reader for button value: %w", err)
	}
	defer r.Close()
	expanded, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("decompress button value: %w", err)
	}
	return expanded, nil
}

// parseActionValue parses a Slack button value into an actionValue.
// Values with the "gz:" prefix are decompressed (gzip + base64url) before JSON parsing.
// This is the counterpart of compressButtonValue in adapter/output/slack/blockkit.go.
func parseActionValue(s string) (actionValue, error) {
	var av actionValue
	data, err := decodeButtonValue(s)
	if err != nil {
		return av, err
	}
	if err := json.Unmarshal(data, &av); err != nil {
		return av, err
	}
	return av, nil
}

// notifyIfTimeout sends a timeout notice to Slack when the operation context expired.
// Uses a fresh 30-second context since the original ctx is already cancelled.
func (h *InteractiveHandler) notifyIfTimeout(ctx context.Context, err error, target port.NotifyTarget) {
	if ctx.Err() != context.DeadlineExceeded {
		return
	}
	freshCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	msg := "⏰ 操作がタイムアウトしました（Slack response_url の有効期限切れ）。GCP コンソールで実際の状態を確認してください。"
	if ferr := h.notifier.UpdateMessage(freshCtx, target, msg); ferr != nil {
		slog.Error("timeout fallback notification also failed", "err", ferr)
	}
}

// firstNonEmpty returns a if non-empty, otherwise b.
// Used to prefer plural (new) fields while falling back to singular (legacy) fields.
func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
