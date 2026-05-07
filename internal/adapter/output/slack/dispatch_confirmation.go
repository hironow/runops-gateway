package slack

// DispatchConfirmation is the input to BuildDispatchConfirmation. It mirrors
// the on-the-wire dispatchActionValue declared in input/slack but lives here
// so the Block Kit builder has no inbound dependency on the input package.
//
// The button payload (ApproveValue / DenyValue) is constructed by the caller
// (the Slash Command handler) using its own dispatchActionValue type and
// CompressButtonValue. We keep this struct minimal — only the fields the
// builder needs to render the visible message.
type DispatchConfirmation struct {
	// Role is the agent role being dispatched to ("paintress" / "sightjack" / ...).
	Role string
	// Text is the dispatch instruction. Truncated for display via safeTrunc;
	// the full value travels in ApproveValue (compressed).
	Text string
	// RequesterID is the Slack user ID of the operator who typed /agent.
	RequesterID string
	// IdempotencyKey is shown in the confirmation so the operator can match it
	// against the eventual ack message.
	IdempotencyKey string
	// ProjectID is the multiplex project_id (#0008). When non-empty it is
	// rendered as an extra "Project" line so the operator can sanity-check
	// the routing before approving. Empty values are omitted from the
	// rendered message to keep the existing single-project layout.
	ProjectID string
	// ApproveValue is the full payload to embed in the Approve button's
	// value field (must already be compressed via CompressButtonValue).
	ApproveValue string
	// DenyValue is the corresponding payload for the Deny button.
	DenyValue string
}

// BuildDispatchConfirmation returns a Block Kit message asking the operator to
// confirm an /agent dispatch. The message is shown ephemerally (response_type
// "ephemeral") so it does not pollute the channel before the operator decides.
//
// Implements F-5 from docs/handover.md: keep a human-in-the-loop step between
// /agent input and actual agent execution, so that a misclick or paste cannot
// directly trigger a destructive operation.
func BuildDispatchConfirmation(p DispatchConfirmation) SlackPayload {
	// 依頼者は <@U...> 形式で出力すると Slack が @username に展開する。
	// 他のフィールドは識別子なので code-formatted (バッククォート) で残す。
	detail := "*エージェント:* `" + safeTrunc(p.Role, 50) + "`\n" +
		"*依頼者:* <@" + safeTrunc(p.RequesterID, 50) + ">\n"
	if p.ProjectID != "" {
		detail += "*Project:* `" + safeTrunc(p.ProjectID, 64) + "`\n"
	}
	detail += "*指示内容:* `" + safeTrunc(p.Text, 500) + "`\n" +
		"*依頼ID:* `" + safeTrunc(p.IdempotencyKey, 64) + "`"

	approve := NewButton("dispatch_approve", "✅ Dispatch", p.ApproveValue, "primary").
		WithConfirm(
			"この dispatch を実行しますか?",
			"AI agent が承認後すぐに作業を開始します。誤入力でないか必ず確認してください。",
			"はい、実行します",
			"キャンセル",
		)
	deny := NewButton("dispatch_deny", "🚫 Cancel", p.DenyValue, "danger")

	return SlackPayload{
		ResponseType: "ephemeral",
		Blocks: []Block{
			HeaderBlock("🤖 エージェント dispatch の確認"),
			SectionBlock(detail),
			DividerBlock(),
			ActionsBlock(approve, deny),
		},
	}
}
