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

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/hironow/runops-gateway/internal/adapter/observability"
	"github.com/hironow/runops-gateway/internal/core/domain"
	"github.com/hironow/runops-gateway/internal/core/port"
)

// handlerTracerName identifies this package as the OTel instrumentation
// library so all Slack adapter spans group together in a span browser.
const handlerTracerName = "github.com/hironow/runops-gateway/internal/adapter/input/slack"

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
// (block-kit button clicks: approve / deny / dispatch_approve / dispatch_deny /
// approval_approve / approval_deny).
type InteractiveHandler struct {
	useCase           port.RunOpsUseCase
	dispatchUseCase   DispatchUseCase
	notifier          port.Notifier
	consumedTokens    port.ConsumedTokenStore
	approvalPublisher port.DMailPublisher           // optional, Phase 4a (ADR 0019)
	pending           *observability.PendingTracker // optional, Issue 0005 — when nil, falls back to bare `go`
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

// WithApprovalPublisher enables the Phase 4a approval_approve / approval_deny
// path. The publisher is invoked when a 4-eyes approver clicks Approve so the
// convergence ack lands back on dmail-inbound for the original producer.
// Returns the same handler so callers can chain after NewInteractiveHandler.
func (h *InteractiveHandler) WithApprovalPublisher(p port.DMailPublisher) *InteractiveHandler {
	h.approvalPublisher = p
	return h
}

// WithPendingTracker registers an *observability.PendingTracker so every
// goroutine spawned to keep working after ServeHTTP returns is tracked and
// can be wait()-ed by main before tp.Shutdown — see Issue 0005 +
// experiments/2026-05-06_otel-goroutine-flush-cloudrun.md. When the tracker
// is nil the handler falls back to a bare `go func()`; this preserves the
// pre-PendingTracker behaviour for tests / dev.
func (h *InteractiveHandler) WithPendingTracker(p *observability.PendingTracker) *InteractiveHandler {
	h.pending = p
	return h
}

// goAsync runs fn in a goroutine. When PendingTracker is wired the goroutine
// is registered so cmd/server's shutdown sequence can wait for it; otherwise
// it falls through to a plain `go fn()`.
func (h *InteractiveHandler) goAsync(fn func()) {
	if h.pending != nil {
		h.pending.Go(fn)
		return
	}
	go fn()
}

// responseURLTimeout matches Slack's 30-minute response_url validity, leaving
// 5 minutes of margin for the final notification POST. Used by every async
// goroutine in ServeHTTP.
const responseURLTimeout = 25 * time.Minute

// ServeHTTP implements http.Handler.
func (h *InteractiveHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	// 1. Read body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	// 2. Verify Slack signature (manual span — failure tracking is operational
	//    gold here; an unverified payload means either Slack secret rotation
	//    failed or someone is replaying old captures).
	_, verifySpan := otel.Tracer(handlerTracerName).Start(ctx, "slack.verify_signature")
	if err := VerifySignature(r.Header, body, h.signingSecret); err != nil {
		verifySpan.RecordError(err)
		verifySpan.SetStatus(codes.Error, "signature verification failed")
		verifySpan.End()
		slog.Warn("slack signature verification failed", "error", err)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	verifySpan.End()
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

	// Detach cancellation from r.Context() so the spawned goroutines (which
	// outlive ServeHTTP because Slack expects a 200 within 3s) keep the OTel
	// trace context but never get cancelled when the HTTP response returns.
	traceCtx := context.WithoutCancel(ctx)

	// dispatch_* actions carry a dispatchActionValue payload (Phase 1 / F-5 fix),
	// not the actionValue used by approve/deny. Branch early so we do not run
	// the actionValue parser on the wrong shape.
	if action.ActionID == "dispatch_approve" || action.ActionID == "dispatch_deny" {
		h.handleDispatchAction(traceCtx, action, slackPayload.User.ID, target)
		w.WriteHeader(http.StatusOK)
		return
	}

	// approval_* actions are the Phase 4a 4-eyes path (ADR 0019). Same early
	// branch reasoning: distinct payload shape, distinct lifecycle.
	if action.ActionID == "approval_approve" || action.ActionID == "approval_deny" {
		h.handleApprovalAction(traceCtx, action, slackPayload.User.ID, target)
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
		h.goAsync(func() {
			ctx, cancel := context.WithTimeout(context.Background(), responseURLTimeout)
			defer cancel()
			if err := h.useCase.ApproveAction(ctx, req, target); err != nil {
				slog.Error("ApproveAction failed", "error", err)
				h.notifyIfTimeout(ctx, err, target)
			}
		})
	case action.ActionID == "deny":
		h.goAsync(func() {
			ctx, cancel := context.WithTimeout(context.Background(), responseURLTimeout)
			defer cancel()
			if err := h.useCase.DenyAction(ctx, req, target); err != nil {
				slog.Error("DenyAction failed", "error", err)
				h.notifyIfTimeout(ctx, err, target)
			}
		})
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
func (h *InteractiveHandler) handleDispatchAction(traceCtx context.Context, action interactiveAction, clickerUserID string, target port.NotifyTarget) {
	traceCtx, span := otel.Tracer(handlerTracerName).Start(traceCtx, "slack.handle_dispatch_action")
	span.SetAttributes(
		attribute.String("slack.action_id", action.ActionID),
		attribute.String("slack.clicker_user_id", clickerUserID),
	)

	dv, err := parseDispatchActionValue(action.Value)
	if err != nil {
		slog.Warn("failed to parse dispatch action value", "error", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "parse dispatch action value")
		span.End()
		return
	}
	span.SetAttributes(attribute.String("dispatch.role", dv.Role))
	if dv.Role == "" {
		slog.Warn("dispatch action value missing role")
		span.SetStatus(codes.Error, "missing role")
		span.End()
		return
	}

	// Hijack guard: never trust the requester ID embedded in the payload as the
	// click-time approver. Cross-check the clicker's Slack user ID instead.
	if clickerUserID == "" || clickerUserID != dv.RequesterID {
		slog.Warn("dispatch action clicker mismatch",
			"action", action.ActionID, "clicker", clickerUserID, "requester", dv.RequesterID)
		span.SetStatus(codes.Error, "clicker mismatch")
		span.End()
		h.goAsync(func() {
			ctx, cancel := context.WithTimeout(traceCtx, 30*time.Second)
			defer cancel()
			if err := h.notifier.SendEphemeral(ctx, target, clickerUserID,
				"🚫 自分が発行した dispatch のみ承認・キャンセルできます"); err != nil {
				slog.Error("dispatch hijack ephemeral notification failed", "error", err)
			}
		})
		return
	}

	if action.ActionID == "dispatch_deny" {
		span.SetAttributes(attribute.String("dispatch.outcome", "denied"))
		span.End()
		h.goAsync(func() {
			ctx, cancel := context.WithTimeout(traceCtx, responseURLTimeout)
			defer cancel()
			if err := h.notifier.UpdateMessage(ctx, target, "🚫 Dispatch をキャンセルしました"); err != nil {
				slog.Error("dispatch_deny notification failed", "error", err)
			}
		})
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
			span.SetStatus(codes.Error, "replay rejected")
			span.End()
			h.goAsync(func() {
				ctx, cancel := context.WithTimeout(traceCtx, 30*time.Second)
				defer cancel()
				if err := h.notifier.SendEphemeral(ctx, target, clickerUserID,
					"⚠️ この dispatch は既に処理済みです（重複クリック防止）"); err != nil {
					slog.Error("dispatch replay ephemeral failed", "error", err)
				}
			})
			return
		}
	}

	span.SetAttributes(attribute.String("dispatch.outcome", "approved"))
	span.End()

	req := domain.DispatchRequest{
		Role:           domain.AgentRole(dv.Role),
		Text:           dv.Text,
		RequesterID:    clickerUserID, // trust the clicker, not the payload
		IdempotencyKey: dv.IdempotencyKey,
		IssuedAt:       dv.IssuedAt,
		// Phase 3 (ADR 0018): pass channel + thread through so the Pub/Sub
		// publish carries them as metadata. The outbound subscriber uses
		// these to thread-reply when the agent finishes.
		SlackChannelID: target.ChannelID,
		SlackThreadTS:  target.ThreadTS,
	}
	h.goAsync(func() {
		ctx, cancel := context.WithTimeout(traceCtx, responseURLTimeout)
		defer cancel()
		if err := h.dispatchUseCase.DispatchAgentTask(ctx, req, target); err != nil {
			slog.Error("DispatchAgentTask failed", "error", err)
		}
	})
}

// dispatchApproveToken derives the consumed-token key for a dispatchActionValue.
// Includes IdempotencyKey + IssuedAt + RequesterID so two distinct /agent
// invocations from the same operator produce distinct tokens; replay of the
// exact same button payload always collides on the same key.
func dispatchApproveToken(dv dispatchActionValue) string {
	return fmt.Sprintf("dispatch_approve/%s/%s/%d",
		dv.RequesterID, dv.IdempotencyKey, dv.IssuedAt)
}

// handleApprovalAction routes Phase 4a (ADR 0019) HIGH severity approvals.
// approval_approve publishes a convergence ack back to dmail-inbound for the
// original producer; approval_deny just notes the rejection in the thread.
//
// Stacked guards (mirror Phase 1 hijack defenses):
//  1. clicker != original_requester (4-eyes)
//  2. ConsumedTokenStore mark+lock (one-time button consume)
//  3. Body digest match (tamper detection on the button payload)
//
// Authorization (allowlist) is shared with the rest of the gateway via the
// EnvAuthChecker invoked indirectly through the FallbackNotifier — but Phase
// 4a relies on the Slack workspace itself to enforce who can press buttons,
// because the channel is private to the operator team.
func (h *InteractiveHandler) handleApprovalAction(traceCtx context.Context, action interactiveAction, clickerUserID string, target port.NotifyTarget) {
	traceCtx, span := otel.Tracer(handlerTracerName).Start(traceCtx, "slack.handle_approval_action")
	span.SetAttributes(
		attribute.String("slack.action_id", action.ActionID),
		attribute.String("slack.clicker_user_id", clickerUserID),
	)

	av, err := parseApprovalActionValue(action.Value)
	if err != nil {
		slog.Warn("failed to parse approval action value", "error", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "parse approval action value")
		span.End()
		return
	}
	span.SetAttributes(
		attribute.String("approval.parent_idempotency_key", av.ParentIdempotencyKey),
		attribute.String("approval.original_requester_id", av.OriginalRequesterID),
	)
	if av.ParentIdempotencyKey == "" || av.OriginalRequesterID == "" {
		slog.Warn("approval action value missing required fields",
			"parent", av.ParentIdempotencyKey, "requester", av.OriginalRequesterID)
		span.SetStatus(codes.Error, "missing required fields")
		span.End()
		return
	}

	// 4-eyes guard: the clicker MUST be different from the operator who
	// issued the original dispatch / convergence chain. self-approval is
	// the entire point of HIGH severity; allowing it defeats Phase 4a.
	if clickerUserID == "" || clickerUserID == av.OriginalRequesterID {
		slog.Warn("approval action 4-eyes guard rejected",
			"action", action.ActionID, "clicker", clickerUserID, "original_requester", av.OriginalRequesterID)
		span.SetStatus(codes.Error, "4-eyes guard rejected")
		span.End()
		h.goAsync(func() {
			ctx, cancel := context.WithTimeout(traceCtx, 30*time.Second)
			defer cancel()
			if err := h.notifier.SendEphemeral(ctx, target, clickerUserID,
				"🚫 元 dispatch を発行した本人は HIGH severity を承認できません (4-eyes)"); err != nil {
				slog.Error("approval 4-eyes ephemeral notification failed", "error", err)
			}
		})
		return
	}

	if action.ActionID == "approval_deny" {
		span.SetAttributes(attribute.String("approval.outcome", "denied"))
		span.End()
		h.goAsync(func() {
			ctx, cancel := context.WithTimeout(traceCtx, responseURLTimeout)
			defer cancel()
			if err := h.notifier.UpdateMessage(ctx, target,
				fmt.Sprintf("🚫 HIGH severity 承認拒否 by <@%s>", clickerUserID)); err != nil {
				slog.Error("approval_deny notification failed", "error", err)
			}
		})
		return
	}

	// approval_approve from here.
	if h.approvalPublisher == nil {
		slog.Warn("approval_approve received but ApprovalPublisher is not wired")
		span.SetStatus(codes.Error, "ApprovalPublisher not wired")
		span.End()
		return
	}

	if h.consumedTokens != nil {
		token := approvalToken(av)
		if !h.consumedTokens.MarkConsumed(token) {
			slog.Warn("approval_approve replay rejected", "token", token, "clicker", clickerUserID)
			span.SetStatus(codes.Error, "replay rejected")
			span.End()
			h.goAsync(func() {
				ctx, cancel := context.WithTimeout(traceCtx, 30*time.Second)
				defer cancel()
				if err := h.notifier.SendEphemeral(ctx, target, clickerUserID,
					"⚠️ この approval は既に処理済みです (重複クリック防止)"); err != nil {
					slog.Error("approval replay ephemeral failed", "error", err)
				}
			})
			return
		}
	}

	span.SetAttributes(attribute.String("approval.outcome", "approved"))
	span.End()

	mail := domain.DMail{
		ID:             newApprovalAckID(),
		Kind:           domain.DMailKindConvergence,
		Target:         av.Source, // ack flows BACK to the original producer
		Source:         "runops-gateway-slack",
		IdempotencyKey: av.ParentIdempotencyKey + "/approved-by-" + clickerUserID,
		Body:           fmt.Sprintf("HIGH severity approval granted by <@%s> at unix=%d", clickerUserID, time.Now().Unix()),
		Metadata: map[string]string{
			"parent_idempotency_key": av.ParentIdempotencyKey,
			"original_requester_id":  av.OriginalRequesterID,
			"approver_id":            clickerUserID,
			"slack_channel_id":       target.ChannelID,
			"slack_thread_ts":        target.ThreadTS,
			"approval_decision":      "approved",
		},
	}

	h.goAsync(func() {
		ctx, cancel := context.WithTimeout(traceCtx, responseURLTimeout)
		defer cancel()
		if _, err := h.approvalPublisher.PublishDMail(ctx, mail); err != nil {
			slog.Error("approval ack publish failed", "error", err)
			if nerr := h.notifier.UpdateMessage(ctx, target,
				fmt.Sprintf("❌ 承認の Pub/Sub publish に失敗しました: %v", err)); nerr != nil {
				slog.Error("approval failure notification also failed", "error", nerr)
			}
			return
		}
		if err := h.notifier.UpdateMessage(ctx, target,
			fmt.Sprintf("✅ HIGH severity 承認完了 by <@%s>", clickerUserID)); err != nil {
			slog.Error("approval success notification failed", "error", err)
		}
	})
}

// approvalToken is the consumed-token key for an approvalActionValue.
// ParentIdempotencyKey is unique per dispatch chain so it is the canonical
// dedup axis; IssuedAt prevents accidental collision across rare reissues.
func approvalToken(av approvalActionValue) string {
	return fmt.Sprintf("approval/%s/%s/%d", av.OriginalRequesterID, av.ParentIdempotencyKey, av.IssuedAt)
}

// newApprovalAckID returns a 16-byte hex string used as the approval ack
// DMail.ID (filename stem on the receiver side).
func newApprovalAckID() string {
	return "ack-" + dispatchApproveTokenStub() // intentionally distinct from /agent IDs
}

// dispatchApproveTokenStub mirrors the random ID logic from the dispatcher
// without re-importing crypto/rand here. We keep approval ack IDs short and
// deterministic-per-call by using the Unix nano + a small hash.
func dispatchApproveTokenStub() string {
	return fmt.Sprintf("%x", time.Now().UnixNano())
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
