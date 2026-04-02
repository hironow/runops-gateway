package slack

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/hironow/runops-gateway/internal/core/domain"
)

// Slack Block Kit field length limits (docs.slack.dev/reference/block-kit, 2026-03).
const (
	maxHeaderText  = 150  // header block plain_text
	maxSectionText = 3000 // section / mrkdwn text
	maxButtonValue = 2000 // button element value
	maxButtonLabel = 75   // button element text.text
)

// compressButtonValue always compresses s with gzip + base64url (prefix "gz:").
// Compression is unconditional so that parseActionValue in the handler is exercised
// on every button click — bugs in the round-trip are caught immediately in tests and
// production rather than only when the bundle size happens to exceed maxButtonValue.
// On compression error the plain JSON is returned as a fallback; buttonValueError in
// notifier.go will detect if the result still exceeds the 2,000-char Slack limit.
func compressButtonValue(s string) string {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write([]byte(s)); err != nil {
		slog.Warn("gzip write failed for button value", "err", err, "len", len(s))
		return s
	}
	if err := w.Close(); err != nil {
		slog.Warn("gzip flush failed for button value", "err", err, "len", len(s))
		return s
	}
	encoded := "gz:" + base64.RawURLEncoding.EncodeToString(buf.Bytes())
	if len(encoded) > maxButtonValue {
		slog.Warn("button value exceeds Slack limit even after compression; reduce service bundle size",
			"original_len", len(s), "compressed_len", len(encoded), "limit", maxButtonValue)
	}
	return encoded
}

// safeTrunc truncates s to at most max runes, appending "…" if truncated.
// Use this before embedding user-controlled strings into Block Kit fields to
// guarantee Slack API limits are respected.
func safeTrunc(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max-1]) + "…"
}

// Environment indicator image URLs (replace with GCS-hosted images in production).
var environmentImages = map[string]string{
	"production":  "https://placehold.co/75x75/FF0000/FFFFFF?text=PROD",
	"staging":     "https://placehold.co/75x75/FFA500/FFFFFF?text=STG",
	"development": "https://placehold.co/75x75/008000/FFFFFF?text=DEV",
}

// DefaultEnvironmentImage is used when env is not recognized.
const DefaultEnvironmentImage = "https://placehold.co/75x75/808080/FFFFFF?text=ENV"

// EnvironmentImageURL returns the indicator image URL for the given environment name.
func EnvironmentImageURL(env string) string {
	if url, ok := environmentImages[env]; ok {
		return url
	}
	return DefaultEnvironmentImage
}

// DeploymentPayload holds the data needed to build a Slack approval message.
type DeploymentPayload struct {
	Environment    string // "production", "staging", "development"
	ResourceType   string // "service", "job", "worker-pool"
	ResourceName   string // e.g. "frontend-service"
	Target         string // revision name (empty for jobs)
	Action         string // e.g. "canary_10"
	BuildInfo      string // e.g. "main @ a1b2c3d"
	IssuedAt       time.Time
	ApproveValue   string // JSON string for approve button value
	DenyValue      string // JSON string for deny button value
	RequireConfirm bool   // when true, approve button shows a confirm dialog
}

// BuildApprovalMessage constructs a Block Kit payload for the approval request message.
// The returned map can be JSON-marshalled and sent to Slack.
func BuildApprovalMessage(p DeploymentPayload) map[string]any {
	expiry := p.IssuedAt.Add(2 * time.Hour)
	imageURL := EnvironmentImageURL(p.Environment)

	// Each field is individually bounded so the combined detailText stays well
	// under the 3,000-rune section text limit.
	detailText := "*環境:* `" + safeTrunc(p.Environment, 50) + "`\n" +
		"*リソース種別:* " + safeTrunc(p.ResourceType, 20) + "\n" +
		"*リソース名:* `" + safeTrunc(p.ResourceName, 500) + "`\n" +
		"*対象:* `" + safeTrunc(p.Target, 500) + "`\n" +
		"*アクション:* " + safeTrunc(p.Action, 50) + "\n" +
		"*ビルド:* " + safeTrunc(p.BuildInfo, 200) + "\n" +
		"*発行:* " + p.IssuedAt.Format(time.RFC3339) + "\n" +
		"*有効期限:* " + expiry.Format(time.RFC3339)

	return map[string]any{
		"blocks": []map[string]any{
			{
				"type": "header",
				"text": map[string]any{
					"type":  "plain_text",
					"text":  "🚀 デプロイ承認リクエスト",
					"emoji": true,
				},
			},
			{
				"type": "section",
				"text": map[string]any{
					"type": "mrkdwn",
					"text": detailText,
				},
				"accessory": map[string]any{
					"type":      "image",
					"image_url": imageURL,
					"alt_text":  p.Environment + " environment",
				},
			},
			{"type": "divider"},
			{
				"type": "actions",
				"elements": []map[string]any{
					buildApproveButton(p.ApproveValue, p.RequireConfirm),
					{
						"type":      "button",
						"action_id": "deny",
						"style":     "danger",
						"text":      map[string]any{"type": "plain_text", "emoji": true, "text": "🚫 Deny"},
						"value":     p.DenyValue,
					},
				},
			},
		},
	}
}

// BuildCompletionMessage constructs a Block Kit payload after the operation completes.
// It does NOT include action buttons (prevents double-execution).
func BuildCompletionMessage(approverID, summary, env string) map[string]any {
	// Reserve ~80 runes for the fixed prefix/suffix around summary.
	body := "✅ *承認済み・実行完了*\n" + safeTrunc(summary, maxSectionText-80) + "\n承認者: <@" + approverID + ">"
	return map[string]any{
		"replace_original": true,
		"blocks": []map[string]any{
			{
				"type": "section",
				"text": map[string]any{
					"type": "mrkdwn",
					"text": body,
				},
				"accessory": map[string]any{
					"type":      "image",
					"image_url": EnvironmentImageURL(env),
					"alt_text":  env + " environment",
				},
			},
		},
	}
}

// BuildDenialMessage constructs a Block Kit payload after the operation is denied.
func BuildDenialMessage(denierID, summary string) map[string]any {
	body := "🚫 *拒否済み*\n" + safeTrunc(summary, maxSectionText-80) + "\n拒否者: <@" + denierID + ">"
	return map[string]any{
		"replace_original": true,
		"blocks": []map[string]any{
			{
				"type": "section",
				"text": map[string]any{
					"type": "mrkdwn",
					"text": body,
				},
			},
		},
	}
}

// BuildProgressMessage constructs a Block Kit payload for a mid-rollout progress message.
// If nextReq is non-nil, an "advance" button is added. If stopReq is also non-nil, a
// "stop / rollback" or "deny" button is added alongside it.
func BuildProgressMessage(summary string, nextReq *domain.ApprovalRequest, stopReq *domain.ApprovalRequest) map[string]any {
	blocks := []map[string]any{
		{
			"type": "section",
			"text": map[string]any{"type": "mrkdwn", "text": safeTrunc(summary, maxSectionText)},
		},
	}

	if nextReq != nil {
		elements := []map[string]any{
			{
				"type":      "button",
				"action_id": "approve",
				"style":     "primary",
				"text":      map[string]any{"type": "plain_text", "emoji": true, "text": canaryBtnLabel(nextReq)},
				"value":     marshalActionValue(nextReq),
			},
		}
		if stopReq != nil {
			stopActionID := "approve"
			stopLabel := "🛑 停止・ロールバック"
			if stopReq.Action != "rollback" {
				stopActionID = "deny"
				stopLabel = "🚫 Deny"
			}
			elements = append(elements, map[string]any{
				"type":      "button",
				"action_id": stopActionID,
				"style":     "danger",
				"text":      map[string]any{"type": "plain_text", "emoji": true, "text": stopLabel},
				"value":     marshalActionValue(stopReq),
			})
		}
		blocks = append(blocks, map[string]any{
			"type":     "actions",
			"elements": elements,
		})
	}

	return map[string]any{
		"replace_original": true,
		"blocks":           blocks,
	}
}

// buildApproveButton returns the approve button element, optionally with a confirm dialog.
func buildApproveButton(value string, requireConfirm bool) map[string]any {
	btn := map[string]any{
		"type":      "button",
		"action_id": "approve",
		"style":     "primary",
		"text":      map[string]any{"type": "plain_text", "emoji": true, "text": "✅ Approve"},
		"value":     value,
	}
	if requireConfirm {
		btn["confirm"] = map[string]any{
			"title":   map[string]any{"type": "plain_text", "text": "続行しますか？"},
			"text":    map[string]any{"type": "mrkdwn", "text": "DBマイグレーションを実施しましたか？未実施の場合は先に実行してください。"},
			"confirm": map[string]any{"type": "plain_text", "text": "はい、続行します"},
			"deny":    map[string]any{"type": "plain_text", "text": "キャンセル"},
		}
	}
	return btn
}

// canaryBtnLabel returns a human-readable label for the next canary step button.
func canaryBtnLabel(req *domain.ApprovalRequest) string {
	act, err := domain.ParseAction(req.Action)
	if err != nil || act.Percent == 0 {
		return "✅ Canary"
	}
	return fmt.Sprintf("✅ %d%% に昇格", act.Percent)
}

// progressActionValue mirrors the handler's actionValue for button serialization.
type progressActionValue struct {
	ResourceType     string `json:"resource_type"`
	ResourceNames    string `json:"resource_names"`
	Targets          string `json:"targets"`
	Action           string `json:"action"`
	IssuedAt         int64  `json:"issued_at"`
	MigrationDone    bool   `json:"migration_done"`
	NextServiceNames string `json:"next_service_names,omitempty"`
	NextRevisions    string `json:"next_revisions,omitempty"`
	NextAction       string `json:"next_action,omitempty"`
}

// marshalActionValue serializes an ApprovalRequest into the Slack button value JSON,
// then always compresses the result via compressButtonValue (gzip + base64url, prefix "gz:").
// The handler in adapter/input/slack/handler.go decompresses the value transparently.
// See ADR 0011 for the rationale behind unconditional compression.
func marshalActionValue(req *domain.ApprovalRequest) string {
	v := progressActionValue{
		ResourceType:     string(req.ResourceType),
		ResourceNames:    req.ResourceNames,
		Targets:          req.Targets,
		Action:           req.Action,
		IssuedAt:         req.IssuedAt,
		MigrationDone:    req.MigrationDone,
		NextServiceNames: req.NextServiceNames,
		NextRevisions:    req.NextRevisions,
		NextAction:       req.NextAction,
	}
	b, _ := json.Marshal(v)
	return compressButtonValue(string(b))
}
