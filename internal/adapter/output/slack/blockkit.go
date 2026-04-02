package slack

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/hironow/runops-gateway/internal/core/domain"
)

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

	detailText := "*環境:* `" + p.Environment + "`\n" +
		"*リソース種別:* " + p.ResourceType + "\n" +
		"*リソース名:* `" + p.ResourceName + "`\n" +
		"*対象:* `" + p.Target + "`\n" +
		"*アクション:* " + p.Action + "\n" +
		"*ビルド:* " + p.BuildInfo + "\n" +
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
	return map[string]any{
		"replace_original": true,
		"blocks": []map[string]any{
			{
				"type": "section",
				"text": map[string]any{
					"type": "mrkdwn",
					"text": "✅ *承認済み・実行完了*\n" + summary + "\n承認者: <@" + approverID + ">",
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
	return map[string]any{
		"replace_original": true,
		"blocks": []map[string]any{
			{
				"type": "section",
				"text": map[string]any{
					"type": "mrkdwn",
					"text": "🚫 *拒否済み*\n" + summary + "\n拒否者: <@" + denierID + ">",
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
			"text": map[string]any{"type": "mrkdwn", "text": summary},
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
	ResourceType    string `json:"resource_type"`
	ResourceName    string `json:"resource_name"`
	Target          string `json:"target"`
	Action          string `json:"action"`
	IssuedAt        int64  `json:"issued_at"`
	MigrationDone   bool   `json:"migration_done"`
	NextServiceName string `json:"next_service_name,omitempty"`
	NextRevision    string `json:"next_revision,omitempty"`
	NextAction      string `json:"next_action,omitempty"`
}

// marshalActionValue serializes an ApprovalRequest into the Slack button value JSON.
func marshalActionValue(req *domain.ApprovalRequest) string {
	v := progressActionValue{
		ResourceType:    string(req.ResourceType),
		ResourceName:    req.ResourceName,
		Target:          req.Target,
		Action:          req.Action,
		IssuedAt:        req.IssuedAt,
		MigrationDone:   req.MigrationDone,
		NextServiceName: req.NextServiceName,
		NextRevision:    req.NextRevision,
		NextAction:      req.NextAction,
	}
	b, _ := json.Marshal(v)
	return string(b)
}
