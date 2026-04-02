package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

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
