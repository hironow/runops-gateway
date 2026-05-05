package slack

import (
	"strings"
	"testing"
)

func TestBuildApprovalRequest_HasApproveAndDenyButtons(t *testing.T) {
	in := ApprovalRequest{
		Source:               "amadeus",
		Target:               "sightjack",
		OriginalRequesterID:  "U_ORIG",
		ParentIdempotencyKey: "parent-001",
		Body:                 "ADR-003 violation detected in module X.",
		ApproveValue:         "gz:approve-fake",
		DenyValue:            "gz:deny-fake",
	}
	payload := BuildApprovalRequest(in)

	// Must NOT be ephemeral — 4-eyes requires the channel to see the pending request.
	if payload.ResponseType == "ephemeral" {
		t.Errorf("ResponseType should NOT be ephemeral for HIGH severity approval; got %q", payload.ResponseType)
	}

	var foundApprove, foundDeny bool
	for _, b := range payload.Blocks {
		for _, el := range b.Elements {
			if el.ActionID == "approval_approve" {
				foundApprove = true
				if el.Value != in.ApproveValue {
					t.Errorf("approve value drifted: %q", el.Value)
				}
				if el.Confirm == nil {
					t.Error("approval_approve must have a confirm dialog (4-eyes safeguard)")
				}
			}
			if el.ActionID == "approval_deny" {
				foundDeny = true
				if el.Value != in.DenyValue {
					t.Errorf("deny value drifted: %q", el.Value)
				}
			}
		}
	}
	if !foundApprove {
		t.Error("approval_approve button missing")
	}
	if !foundDeny {
		t.Error("approval_deny button missing")
	}
}

func TestBuildApprovalRequest_DisplaysFourEyesGuardance(t *testing.T) {
	in := ApprovalRequest{
		Source:               "amadeus",
		Target:               "sightjack",
		OriginalRequesterID:  "U_ORIG_VISIBLE",
		ParentIdempotencyKey: "parent-001",
		Body:                 "details body",
		ApproveValue:         "gz:x",
		DenyValue:            "gz:y",
	}
	payload := BuildApprovalRequest(in)

	var fullText string
	for _, b := range payload.Blocks {
		if b.Text != nil {
			fullText += b.Text.Text + "\n"
		}
	}
	if !strings.Contains(fullText, "U_ORIG_VISIBLE") {
		t.Errorf("rendered message must surface the original requester so reviewers know who NOT to approve as; got:\n%s", fullText)
	}
	if !strings.Contains(fullText, "amadeus") || !strings.Contains(fullText, "sightjack") {
		t.Errorf("rendered message must show source + target; got:\n%s", fullText)
	}
	if !strings.Contains(fullText, "details body") {
		t.Errorf("rendered message must include the producer body; got:\n%s", fullText)
	}
	// 4-eyes guidance text must appear so reviewers see the rule.
	if !strings.Contains(fullText, "4-eyes") {
		t.Errorf("rendered message should reference 4-eyes; got:\n%s", fullText)
	}
}
