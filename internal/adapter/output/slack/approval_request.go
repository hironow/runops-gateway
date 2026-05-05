package slack

// ApprovalRequest is the input to BuildApprovalRequest. The button payloads
// (ApproveValue / DenyValue) are constructed by the caller (DispatchResultHandler)
// using approvalActionValue + CompressButtonValue.
type ApprovalRequest struct {
	// Source is the producer of the originating HIGH severity D-Mail
	// (e.g. "amadeus" — the verifier flagging a divergence).
	Source string
	// Target is the role expected to act on the convergence (e.g. "sightjack").
	Target string
	// OriginalRequesterID is the Slack user.id of the operator who started
	// the dispatch chain. Shown so reviewers know who must NOT be the approver.
	OriginalRequesterID string
	// ParentIdempotencyKey ties this approval back to the original dispatch
	// so audit logs and one-time consume guards line up.
	ParentIdempotencyKey string
	// Body is the human-readable explanation from the producer (typically
	// amadeus's divergence summary).
	Body string
	// ApproveValue is the compressed approvalActionValue for the Approve button.
	// Must already be CompressButtonValue-encoded.
	ApproveValue string
	// DenyValue is the compressed approvalActionValue for the Deny button.
	DenyValue string
}

// BuildApprovalRequest renders a HIGH severity convergence into a Block Kit
// message that demands a SECOND operator's approval before the producer's
// next step proceeds. ADR 0019.
//
// The message is posted in_channel (not ephemeral) so the whole team can see
// that an approval is pending — that is the entire point of 4-eyes.
func BuildApprovalRequest(p ApprovalRequest) SlackPayload {
	detail := "*Source:* `" + safeTrunc(p.Source, 50) + "`\n" +
		"*Target:* `" + safeTrunc(p.Target, 50) + "`\n" +
		"*Original Requester:* `" + safeTrunc(p.OriginalRequesterID, 50) + "` " +
		"_(must NOT be the approver — 4-eyes)_" + "\n" +
		"*Parent ID:* `" + safeTrunc(p.ParentIdempotencyKey, 64) + "`"

	bodyPreview := safeTrunc(p.Body, maxSectionText-200) // leave room for header

	approve := NewButton("approval_approve", "✅ Approve", p.ApproveValue, "primary").
		WithConfirm(
			"この HIGH severity を承認しますか?",
			"承認すると、5本柱の後続処理が進みます。元 dispatch 発行者は承認できません。",
			"はい、承認します",
			"キャンセル",
		)
	deny := NewButton("approval_deny", "🚫 Deny", p.DenyValue, "danger")

	return SlackPayload{
		Blocks: []Block{
			HeaderBlock("🚨 HIGH severity 承認リクエスト (4-eyes)"),
			SectionBlock(detail),
			DividerBlock(),
			SectionBlock("*詳細:*\n" + bodyPreview),
			DividerBlock(),
			ActionsBlock(approve, deny),
		},
	}
}
