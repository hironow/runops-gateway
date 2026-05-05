package slack

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/hironow/runops-gateway/internal/core/domain"
	"github.com/hironow/runops-gateway/internal/core/port"
)

// ApprovalRequester implements port.ApprovalRequester by posting a Block Kit
// approval message via Slack chat.postMessage with a Bot Token (ADR 0019,
// dependent on ADR 0017 for the Bot Token surface).
//
// Distinct from FallbackNotifier because the message shape is fundamentally
// different (Block Kit blocks rather than text + buttons-as-fallback). The
// constructor stays minimal — production wires the same Bot Token used by the
// FallbackNotifier.
type ApprovalRequester struct {
	chatPostMessageURL string
	botToken           string
	httpClient         *http.Client
}

// NewApprovalRequester returns an ApprovalRequester pointed at the given
// chat.postMessage endpoint (production: slack.com/api/chat.postMessage; tests
// inject an httptest server URL).
func NewApprovalRequester(chatPostMessageURL, botToken string) *ApprovalRequester {
	return &ApprovalRequester{
		chatPostMessageURL: chatPostMessageURL,
		botToken:           botToken,
		httpClient:         http.DefaultClient,
	}
}

// PostApprovalRequest renders mail into a Block Kit approval prompt and posts
// it to target.ChannelID / target.ThreadTS. The button payloads embed an
// approvalActionValue (built locally here so we do not pull from input/slack)
// compressed via CompressButtonValue.
func (r *ApprovalRequester) PostApprovalRequest(ctx context.Context, target port.NotifyTarget, mail domain.DMail) error {
	if target.ChannelID == "" {
		return fmt.Errorf("approval requester: target.ChannelID is required")
	}
	if r.botToken == "" {
		return fmt.Errorf("approval requester: bot token not configured")
	}

	approveValue, denyValue, err := r.buildButtonValues(mail)
	if err != nil {
		return fmt.Errorf("approval requester: build button values: %w", err)
	}

	payload := BuildApprovalRequest(ApprovalRequest{
		Source:               mail.Source,
		Target:               mail.Target,
		OriginalRequesterID:  mail.Metadata["requester_id"],
		ParentIdempotencyKey: mail.Metadata["parent_idempotency_key"],
		Body:                 mail.Body,
		ApproveValue:         approveValue,
		DenyValue:            denyValue,
	})

	chatBody := map[string]any{
		"channel": target.ChannelID,
		"blocks":  payload.Blocks,
	}
	if target.ThreadTS != "" {
		chatBody["thread_ts"] = target.ThreadTS
	}

	raw, err := json.Marshal(chatBody)
	if err != nil {
		return fmt.Errorf("approval requester: marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.chatPostMessageURL, bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("approval requester: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", "Bearer "+r.botToken)

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("approval requester: post: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("approval requester: status %d: %s", resp.StatusCode, string(respBody))
	}
	var apiResp struct {
		OK    bool   `json:"ok"`
		Error string `json:"error,omitempty"`
	}
	if err := json.Unmarshal(respBody, &apiResp); err == nil && !apiResp.OK && apiResp.Error != "" {
		return fmt.Errorf("approval requester: chat.postMessage error: %s", apiResp.Error)
	}
	slog.InfoContext(ctx, "approval request posted",
		"channel", target.ChannelID, "thread_ts", target.ThreadTS,
		"source", mail.Source, "target", mail.Target)
	return nil
}

// buildButtonValues serializes the approvalActionValue payload for both
// buttons. Kept package-private because the on-the-wire shape mirrors the
// input/slack package's parser; production callers do not need to construct
// these by hand.
func (r *ApprovalRequester) buildButtonValues(mail domain.DMail) (string, string, error) {
	parent := mail.Metadata["parent_idempotency_key"]
	originalRequester := mail.Metadata["requester_id"]
	if parent == "" || originalRequester == "" {
		return "", "", fmt.Errorf("missing parent_idempotency_key or requester_id metadata")
	}
	digest := approvalDigest(mail.Body)
	value := map[string]any{
		"parent_idempotency_key": parent,
		"original_requester_id":  originalRequester,
		"source":                 mail.Source,
		"target":                 mail.Target,
		"body_digest":            digest,
		"issued_at":              0, // ID is in parent_idempotency_key; issued_at exists for replay-binding only
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return "", "", err
	}
	encoded := CompressButtonValue(string(raw))
	return encoded, encoded, nil
}

// approvalDigest is duplicated from input/slack ApprovalBodyDigest because
// import-cycling output→input is not allowed. The two must stay in sync; a
// mismatch is caught by the integration test that round-trips a real button
// click.
func approvalDigest(body string) string {
	h := sha256.Sum256([]byte(body))
	return hex.EncodeToString(h[:])[:16]
}
