package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/hironow/runops-gateway/internal/core/domain"
	"github.com/hironow/runops-gateway/internal/core/port"
)

// ResponseURLNotifier sends Slack messages via response_url (no Bot Token needed).
type ResponseURLNotifier struct {
	client *http.Client
}

// NewResponseURLNotifier creates a notifier using Slack's response_url mechanism.
func NewResponseURLNotifier() *ResponseURLNotifier {
	return &ResponseURLNotifier{client: http.DefaultClient}
}

// UpdateMessage replaces the original Slack message with a text update.
func (n *ResponseURLNotifier) UpdateMessage(ctx context.Context, target port.NotifyTarget, text string) error {
	if target.Mode == "stdout" {
		slog.InfoContext(ctx, "[stdout notifier] update", "text", text)
		return nil
	}
	payload := map[string]any{
		"replace_original": true,
		"text":             text,
	}
	return n.post(ctx, target.ResponseURL, payload)
}

// ReplaceMessage replaces the original Slack message with rich block content.
func (n *ResponseURLNotifier) ReplaceMessage(ctx context.Context, target port.NotifyTarget, blocks any) error {
	if target.Mode == "stdout" {
		slog.InfoContext(ctx, "[stdout notifier] replace", "blocks", fmt.Sprintf("%v", blocks))
		return nil
	}
	payload := map[string]any{
		"replace_original": true,
		"blocks":           blocks,
	}
	return n.post(ctx, target.ResponseURL, payload)
}

// SendEphemeral sends a message visible only to the specified user.
func (n *ResponseURLNotifier) SendEphemeral(ctx context.Context, target port.NotifyTarget, userID, text string) error {
	if target.Mode == "stdout" {
		slog.WarnContext(ctx, "[stdout notifier] ephemeral", "user", userID, "text", text)
		return nil
	}
	payload := map[string]any{
		"response_type":    "ephemeral",
		"replace_original": false,
		"text":             fmt.Sprintf("<@%s> %s", userID, text),
	}
	return n.post(ctx, target.ResponseURL, payload)
}

// OfferContinuation replaces the message with a completion summary and optional next/stop buttons.
// If any button value would exceed Slack's 2,000-char limit the buttons cannot be used, so an
// explicit error message is posted instead to prevent silent failure.
func (n *ResponseURLNotifier) OfferContinuation(ctx context.Context, target port.NotifyTarget, summary string, nextReq *domain.ApprovalRequest, stopReq *domain.ApprovalRequest) error {
	if target.Mode == "stdout" {
		slog.InfoContext(ctx, "[stdout notifier] continuation", "summary", summary)
		if nextReq != nil {
			slog.InfoContext(ctx, "[stdout notifier] next step available", "action", nextReq.Action)
		}
		return nil
	}

	// Pre-validate button value lengths before building the message.
	// A value that exceeds the Slack limit causes the button to be non-functional
	// (silently broken), so we surface an explicit error to the operator instead.
	if errMsg, over := buttonValueError(nextReq, stopReq); over {
		return n.post(ctx, target.ResponseURL, map[string]any{
			"replace_original": true,
			"text":             errMsg,
		})
	}

	payload := BuildProgressMessage(summary, nextReq, stopReq)
	return n.post(ctx, target.ResponseURL, payload)
}

// buttonValueError checks whether any of the provided requests would produce a button
// value that exceeds Slack's 2,000-character limit.
// Returns the user-facing error message and true when the limit would be exceeded.
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

func (n *ResponseURLNotifier) post(ctx context.Context, url string, payload any) error {
	if url == "" {
		return fmt.Errorf("slack notifier: response_url is empty")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("slack notifier: marshal payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("slack notifier: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := n.client.Do(req)
	if err != nil {
		return fmt.Errorf("slack notifier: post: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("slack notifier: unexpected status %d", resp.StatusCode)
	}
	return nil
}
