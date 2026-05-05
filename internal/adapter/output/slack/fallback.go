package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/hironow/runops-gateway/internal/core/domain"
	"github.com/hironow/runops-gateway/internal/core/port"
)

// FallbackNotifier wraps a primary port.Notifier (typically ResponseURLNotifier)
// and falls back to chat.postMessage when the primary returns an error that
// indicates the Slack response_url has hit its 30-min validity window or
// 5-call usage limit. See ADR 0017 for the rationale.
//
// SendEphemeral does NOT fall back: the response_url is the only Slack feature
// that can post a message visible to the invoker only without a Bot Token's
// chat.postEphemeral scope. Errors from primary.SendEphemeral propagate as is;
// the caller is expected to log and continue.
type FallbackNotifier struct {
	primary           port.Notifier
	chatPostMessageURL string
	botToken          string
	defaultChannelID  string
	httpClient        *http.Client
}

// NewFallbackNotifier constructs a FallbackNotifier.
//
// chatPostMessageURL is normally "https://slack.com/api/chat.postMessage" in
// production; tests inject an httptest.Server URL.
//
// botToken is the xoxb-... OAuth Bot Token (from Secret Manager in production).
//
// defaultChannelID is used only as a documentation aid: NotifyTarget.ChannelID
// is the load-bearing field. Pass empty string if you want to force every
// caller to populate target.ChannelID explicitly.
func NewFallbackNotifier(primary port.Notifier, chatPostMessageURL, botToken, defaultChannelID string) *FallbackNotifier {
	return &FallbackNotifier{
		primary:           primary,
		chatPostMessageURL: chatPostMessageURL,
		botToken:          botToken,
		defaultChannelID:  defaultChannelID,
		httpClient:        http.DefaultClient,
	}
}

// UpdateMessage delegates to the primary, falling back to chat.postMessage on
// response_url limit errors.
func (n *FallbackNotifier) UpdateMessage(ctx context.Context, target port.NotifyTarget, text string) error {
	if target.Mode == port.ModeStdout {
		return n.primary.UpdateMessage(ctx, target, text)
	}
	err := n.primary.UpdateMessage(ctx, target, text)
	if err == nil || !isResponseURLLimitErr(err) {
		return err
	}
	slog.WarnContext(ctx, "primary notifier hit response_url limit; falling back to chat.postMessage",
		"primary_err", err)
	return n.postChatMessage(ctx, target, map[string]any{"text": text})
}

// ReplaceMessage mirrors UpdateMessage's fallback behaviour.
func (n *FallbackNotifier) ReplaceMessage(ctx context.Context, target port.NotifyTarget, text string) error {
	if target.Mode == port.ModeStdout {
		return n.primary.ReplaceMessage(ctx, target, text)
	}
	err := n.primary.ReplaceMessage(ctx, target, text)
	if err == nil || !isResponseURLLimitErr(err) {
		return err
	}
	slog.WarnContext(ctx, "primary ReplaceMessage hit response_url limit; falling back",
		"primary_err", err)
	return n.postChatMessage(ctx, target, map[string]any{"text": text})
}

// SendEphemeral does not fall back (chat.postEphemeral requires extra scopes
// the Bot Token may not have, and the call site treats ephemeral failures as
// non-blocking warnings).
func (n *FallbackNotifier) SendEphemeral(ctx context.Context, target port.NotifyTarget, userID, text string) error {
	return n.primary.SendEphemeral(ctx, target, userID, text)
}

// OfferContinuation falls back by re-posting the summary text via
// chat.postMessage. Buttons cannot be re-presented after response_url expiry —
// the operator must re-trigger the flow if they need to advance further.
func (n *FallbackNotifier) OfferContinuation(ctx context.Context, target port.NotifyTarget, summary string, nextReq *domain.ApprovalRequest, stopReq *domain.ApprovalRequest) error {
	if target.Mode == port.ModeStdout {
		return n.primary.OfferContinuation(ctx, target, summary, nextReq, stopReq)
	}
	err := n.primary.OfferContinuation(ctx, target, summary, nextReq, stopReq)
	if err == nil || !isResponseURLLimitErr(err) {
		return err
	}
	slog.WarnContext(ctx, "primary OfferContinuation hit response_url limit; posting summary via chat.postMessage",
		"primary_err", err)
	body := map[string]any{
		"text": summary + "\n\n⚠️ 続きの操作ボタンは response_url 失効のため表示できません。再度コマンドを実行してください。",
	}
	return n.postChatMessage(ctx, target, body)
}

// RebuildInitialApproval mirrors UpdateMessage's fallback shape: rebuild fails
// because the buttons need a fresh response_url, but at least the operator
// gets the error context posted to the thread.
func (n *FallbackNotifier) RebuildInitialApproval(ctx context.Context, target port.NotifyTarget, errMsg string, jobReq, svcReq, denyReq *domain.ApprovalRequest) error {
	if target.Mode == port.ModeStdout {
		return n.primary.RebuildInitialApproval(ctx, target, errMsg, jobReq, svcReq, denyReq)
	}
	err := n.primary.RebuildInitialApproval(ctx, target, errMsg, jobReq, svcReq, denyReq)
	if err == nil || !isResponseURLLimitErr(err) {
		return err
	}
	slog.WarnContext(ctx, "primary RebuildInitialApproval hit response_url limit; posting plain notice via chat.postMessage",
		"primary_err", err)
	return n.postChatMessage(ctx, target, map[string]any{
		"text": errMsg + "\n\n⚠️ 承認ボタンは response_url 失効のため再表示できません。CI/CD を再実行してください。",
	})
}

// isResponseURLLimitErr returns true when err looks like the 404 Slack returns
// for an expired or over-used response_url. The check is string-based because
// the underlying error originates from ResponseURLNotifier.post which embeds
// the response body verbatim.
func isResponseURLLimitErr(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	if !strings.Contains(s, "404") {
		return false
	}
	for _, signal := range []string{"expired_url", "rate_limited", "channel_not_found", "no_text"} {
		if strings.Contains(s, signal) {
			return true
		}
	}
	// Bare "404" with response_url in the URL is also indicative — Slack
	// sometimes returns 404 with no JSON body when a response_url has been
	// fully consumed.
	return strings.Contains(s, "response_url") || strings.Contains(s, "expired_url")
}

// postChatMessage POSTs a chat.postMessage payload using the Bot Token.
// target.ChannelID is required; we never fall back to a default channel
// because mis-routing a message is worse than failing loudly.
func (n *FallbackNotifier) postChatMessage(ctx context.Context, target port.NotifyTarget, body map[string]any) error {
	channel := target.ChannelID
	if channel == "" {
		channel = n.defaultChannelID
	}
	if channel == "" {
		return fmt.Errorf("slack fallback: cannot post chat.postMessage without a channel ID")
	}
	payload := map[string]any{"channel": channel}
	if target.ThreadTS != "" {
		payload["thread_ts"] = target.ThreadTS
	}
	for k, v := range body {
		payload[k] = v
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("slack fallback: marshal payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.chatPostMessageURL, bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("slack fallback: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", "Bearer "+n.botToken)
	resp, err := n.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("slack fallback: post: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("slack fallback: chat.postMessage status %d: %s", resp.StatusCode, string(respBody))
	}
	// Slack returns 200 with {"ok": false, "error": "..."} for application errors.
	var apiResp struct {
		OK    bool   `json:"ok"`
		Error string `json:"error,omitempty"`
	}
	if err := json.Unmarshal(respBody, &apiResp); err == nil && !apiResp.OK && apiResp.Error != "" {
		return fmt.Errorf("slack fallback: chat.postMessage error: %s", apiResp.Error)
	}
	return nil
}
