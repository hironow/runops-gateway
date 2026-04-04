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
	Environment    string
	ResourceType   string
	ResourceName   string
	Target         string
	Action         string
	BuildInfo      string
	IssuedAt       time.Time
	ApproveValue   string
	DenyValue      string
	RequireConfirm bool
}

// BuildApprovalMessage constructs a Block Kit payload for the approval request message.
func BuildApprovalMessage(p DeploymentPayload) SlackPayload {
	expiry := p.IssuedAt.Add(2 * time.Hour)
	imageURL := EnvironmentImageURL(p.Environment)

	detailText := "*環境:* `" + safeTrunc(p.Environment, 50) + "`\n" +
		"*リソース種別:* " + safeTrunc(p.ResourceType, 20) + "\n" +
		"*リソース名:* `" + safeTrunc(p.ResourceName, 500) + "`\n" +
		"*対象:* `" + safeTrunc(p.Target, 500) + "`\n" +
		"*アクション:* " + safeTrunc(p.Action, 50) + "\n" +
		"*ビルド:* " + safeTrunc(p.BuildInfo, 200) + "\n" +
		"*発行:* " + p.IssuedAt.Format(time.RFC3339) + "\n" +
		"*有効期限:* " + expiry.Format(time.RFC3339)

	approveBtn := NewButton("approve", "✅ Approve", p.ApproveValue, "primary")
	if p.RequireConfirm {
		approveBtn = approveBtn.WithConfirm(
			"続行しますか？",
			"DBマイグレーションを実施しましたか？未実施の場合は先に実行してください。",
			"はい、続行します",
			"キャンセル",
		)
	}
	denyBtn := NewButton("deny", "🚫 Deny", p.DenyValue, "danger")

	return SlackPayload{
		Blocks: []Block{
			HeaderBlock("🚀 デプロイ承認リクエスト"),
			SectionBlockWithAccessory(detailText, imageURL, p.Environment+" environment"),
			DividerBlock(),
			ActionsBlock(approveBtn, denyBtn),
		},
	}
}

// BuildCompletionMessage constructs a Block Kit payload after the operation completes.
func BuildCompletionMessage(approverID, summary, env string) SlackPayload {
	body := "✅ *承認済み・実行完了*\n" + safeTrunc(summary, maxSectionText-80) + "\n承認者: <@" + approverID + ">"
	imageURL := EnvironmentImageURL(env)
	return ReplacePayload(SectionBlockWithAccessory(body, imageURL, env+" environment"))
}

// BuildDenialMessage constructs a Block Kit payload after the operation is denied.
func BuildDenialMessage(denierID, summary string) SlackPayload {
	body := "🚫 *拒否済み*\n" + safeTrunc(summary, maxSectionText-80) + "\n拒否者: <@" + denierID + ">"
	return ReplacePayload(SectionBlock(body))
}

// BuildProgressMessage constructs a Block Kit payload for a mid-rollout progress message.
func BuildProgressMessage(summary string, nextReq *domain.ApprovalRequest, stopReq *domain.ApprovalRequest) SlackPayload {
	blocks := []Block{
		SectionBlock(safeTrunc(summary, maxSectionText)),
	}

	if nextReq != nil {
		nextBtn := NewButton("approve", canaryBtnLabel(nextReq), marshalActionValue(nextReq), "primary")

		buttons := []Button{nextBtn}
		if stopReq != nil {
			stopActionID := "approve"
			stopLabel := "🛑 停止・ロールバック"
			if stopReq.Action != "rollback" {
				stopActionID = "deny"
				stopLabel = "🚫 Deny"
			}
			buttons = append(buttons, NewButton(stopActionID, stopLabel, marshalActionValue(stopReq), "danger"))
		}
		blocks = append(blocks, ActionsBlock(buttons...))
	}

	return ReplacePayload(blocks...)
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
	Project          string `json:"project"`
	Location         string `json:"location"`
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
func marshalActionValue(req *domain.ApprovalRequest) string {
	v := progressActionValue{
		Project:          req.Project,
		Location:         req.Location,
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
