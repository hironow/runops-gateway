package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	return n.post(ctx, target.ResponseURL, TextPayload(text))
}

// ReplaceMessage replaces the original Slack message with a mrkdwn section block.
func (n *ResponseURLNotifier) ReplaceMessage(ctx context.Context, target port.NotifyTarget, text string) error {
	if target.Mode == "stdout" {
		slog.InfoContext(ctx, "[stdout notifier] replace", "text", text)
		return nil
	}
	return n.post(ctx, target.ResponseURL, ReplacePayload(SectionBlock(text)))
}

// SendEphemeral sends a message visible only to the specified user.
func (n *ResponseURLNotifier) SendEphemeral(ctx context.Context, target port.NotifyTarget, userID, text string) error {
	if target.Mode == "stdout" {
		slog.WarnContext(ctx, "[stdout notifier] ephemeral", "user", userID, "text", text)
		return nil
	}
	return n.post(ctx, target.ResponseURL, EphemeralPayload(fmt.Sprintf("<@%s> %s", userID, text)))
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

	if errMsg, over := buttonValueError(nextReq, stopReq); over {
		return n.post(ctx, target.ResponseURL, TextPayload(errMsg))
	}

	payload := BuildProgressMessage(summary, nextReq, stopReq)
	return n.post(ctx, target.ResponseURL, payload)
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

func (n *ResponseURLNotifier) post(ctx context.Context, url string, payload SlackPayload) error {
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
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		slog.Error("slack notifier: error response", "status", resp.StatusCode, "body", string(respBody))
		return fmt.Errorf("slack notifier: unexpected status %d: %s", resp.StatusCode, string(respBody))
	}
	if len(respBody) > 0 {
		respStr := string(respBody)
		if respStr != "ok" && respStr != "" {
			slog.Warn("slack notifier: non-ok response body", "body", respStr)
		}
	}
	return nil
}
