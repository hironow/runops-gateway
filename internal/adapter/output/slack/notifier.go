package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/hironow/runops-gateway/internal/core/domain"
	"github.com/hironow/runops-gateway/internal/core/port"
)

// responseURLHost is the only host Slack ever uses for response_url. The
// response_url rides in the (signature-verified) Slack payload but is still
// attacker-influenced input, so post() rejects any other host before issuing a
// request (defense-in-depth against SSRF / CodeQL go/request-forgery). It is a
// compile-time constant so the host check is a recognized taint barrier.
const responseURLHost = "hooks.slack.com"

// ResponseURLNotifier sends Slack messages via response_url (no Bot Token needed).
type ResponseURLNotifier struct {
	client *http.Client
}

// Option configures a ResponseURLNotifier.
type Option func(*ResponseURLNotifier)

// WithHTTPClient overrides the HTTP client. Tests inject a client whose
// transport routes the (constant) hooks.slack.com URL to an httptest server.
func WithHTTPClient(c *http.Client) Option {
	return func(n *ResponseURLNotifier) {
		if c != nil {
			n.client = c
		}
	}
}

// NewResponseURLNotifier creates a notifier using Slack's response_url mechanism.
func NewResponseURLNotifier(opts ...Option) *ResponseURLNotifier {
	n := &ResponseURLNotifier{client: http.DefaultClient}
	for _, opt := range opts {
		opt(n)
	}
	return n
}

// UpdateMessage replaces the original Slack message with a text update.
func (n *ResponseURLNotifier) UpdateMessage(ctx context.Context, target port.NotifyTarget, text string) error {
	if target.Mode == port.ModeStdout {
		slog.InfoContext(ctx, "[stdout notifier] update", "text", text)
		return nil
	}
	return n.post(ctx, target.CallbackURL, TextPayload(text))
}

// ReplaceMessage replaces the original Slack message with a mrkdwn section block.
func (n *ResponseURLNotifier) ReplaceMessage(ctx context.Context, target port.NotifyTarget, text string) error {
	if target.Mode == port.ModeStdout {
		slog.InfoContext(ctx, "[stdout notifier] replace", "text", text)
		return nil
	}
	return n.post(ctx, target.CallbackURL, ReplacePayload(SectionBlock(text)))
}

// SendEphemeral sends a message visible only to the specified user.
func (n *ResponseURLNotifier) SendEphemeral(ctx context.Context, target port.NotifyTarget, userID, text string) error {
	if target.Mode == port.ModeStdout {
		slog.WarnContext(ctx, "[stdout notifier] ephemeral", "user", userID, "text", text)
		return nil
	}
	return n.post(ctx, target.CallbackURL, EphemeralPayload(fmt.Sprintf("<@%s> %s", userID, text)))
}

// OfferContinuation replaces the message with a completion summary and optional next/stop buttons.
// If any button value would exceed Slack's 2,000-char limit the buttons cannot be used, so an
// explicit error message is posted instead to prevent silent failure.
func (n *ResponseURLNotifier) OfferContinuation(ctx context.Context, target port.NotifyTarget, summary string, nextReq *domain.ApprovalRequest, stopReq *domain.ApprovalRequest) error {
	if target.Mode == port.ModeStdout {
		slog.InfoContext(ctx, "[stdout notifier] continuation", "summary", summary)
		if nextReq != nil {
			slog.InfoContext(ctx, "[stdout notifier] next step available", "action", nextReq.Action)
		}
		return nil
	}

	if errMsg, over := buttonValueError(nextReq, stopReq); over {
		return n.post(ctx, target.CallbackURL, TextPayload(errMsg))
	}

	payload := BuildProgressMessage(summary, nextReq, stopReq)
	return n.post(ctx, target.CallbackURL, payload)
}

// RebuildInitialApproval rewrites the Slack message back to the initial 3-button approval state.
// Called after recoverable errors so the operator can pick a different action (e.g. "Canary skip
// migration" after a backup failure) instead of blindly retrying the failing one.
func (n *ResponseURLNotifier) RebuildInitialApproval(ctx context.Context, target port.NotifyTarget, errMsg string, jobReq, svcReq, denyReq *domain.ApprovalRequest) error {
	if target.Mode == port.ModeStdout {
		slog.InfoContext(ctx, "[stdout notifier] rebuild initial approval", "err", errMsg)
		return nil
	}

	if msg, over := buttonValueError(jobReq, svcReq, denyReq); over {
		return n.post(ctx, target.CallbackURL, TextPayload(msg))
	}

	payload := BuildInitialApprovalMessage(errMsg, jobReq, svcReq, denyReq)
	return n.post(ctx, target.CallbackURL, payload)
}

// buttonValueError checks whether any of the provided requests would produce a button
// value that exceeds Slack's 2,000-character limit.
func buttonValueError(reqs ...*domain.ApprovalRequest) (string, bool) {
	for _, req := range reqs {
		if req == nil {
			continue
		}
		v := marshalActionValue(req)
		if len(v) > maxButtonValue {
			return fmt.Sprintf(
				"⚠️ 操作は完了しましたが、次のステップボタンを生成できませんでした。"+
					"ボタン値が %d 文字で Slack の上限 (%d 文字) を超えています。"+
					"サービス数を減らして再実行してください。リソース: %s",
				len(v), maxButtonValue, req.ResourceNames,
			), true
		}
	}
	return "", false
}

func (n *ResponseURLNotifier) post(ctx context.Context, rawURL string, payload SlackPayload) error {
	if rawURL == "" {
		return fmt.Errorf("slack notifier: response_url is empty")
	}
	// SSRF guard (CodeQL go/request-forgery): response_url is attacker-influenced
	// input (it rides in the Slack payload), so validate the destination host
	// against the allowlist before issuing the request.
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("slack notifier: parse response_url: %w", err)
	}
	if parsed.Scheme != "https" {
		return fmt.Errorf("slack notifier: response_url scheme %q not allowed", parsed.Scheme)
	}
	if parsed.Hostname() != responseURLHost {
		return fmt.Errorf("slack notifier: response_url host %q not allowed (want %s)", parsed.Hostname(), responseURLHost)
	}
	if err := payload.Validate(); err != nil {
		return fmt.Errorf("slack notifier: %w", err)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("slack notifier: marshal payload: %w", err)
	}
	// Build the request from the allowlist-validated parsed URL (not the raw
	// string) so the host check sanitizes the value that reaches the request.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, parsed.String(), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("slack notifier: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := n.client.Do(req)
	if err != nil {
		return fmt.Errorf("slack notifier: post: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		slog.Error("slack notifier: error response", "status", resp.StatusCode, "body", string(respBody))
		return fmt.Errorf("slack notifier: unexpected status %d: %s", resp.StatusCode, string(respBody))
	}
	if len(respBody) > 0 {
		respStr := string(respBody)
		if respStr != "ok" && respStr != `{"ok":true}` && respStr != "" {
			slog.Warn("slack notifier: non-ok response body", "body", respStr)
		}
	}
	return nil
}
