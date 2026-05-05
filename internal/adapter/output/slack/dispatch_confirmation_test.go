package slack

import (
	"strings"
	"testing"
)

func TestBuildDispatchConfirmation_HasApproveAndDenyButtons(t *testing.T) {
	// given
	in := DispatchConfirmation{
		Role:           "paintress",
		Text:           "fix M-42",
		RequesterID:    "U0123ABCD",
		IdempotencyKey: "abcd1234",
		ApproveValue:   "gz:fake-approve",
		DenyValue:      "gz:fake-deny",
	}

	// when
	payload := BuildDispatchConfirmation(in)

	// then — must be ephemeral so the channel does not see it before approval
	if payload.ResponseType != "ephemeral" {
		t.Errorf("ResponseType=%q, want ephemeral", payload.ResponseType)
	}
	// must contain an actions block with both buttons
	var foundApprove, foundDeny bool
	for _, b := range payload.Blocks {
		for _, el := range b.Elements {
			if el.ActionID == "dispatch_approve" {
				foundApprove = true
				if el.Value != in.ApproveValue {
					t.Errorf("approve value drifted: %q", el.Value)
				}
			}
			if el.ActionID == "dispatch_deny" {
				foundDeny = true
				if el.Value != in.DenyValue {
					t.Errorf("deny value drifted: %q", el.Value)
				}
			}
		}
	}
	if !foundApprove {
		t.Error("dispatch_approve button missing")
	}
	if !foundDeny {
		t.Error("dispatch_deny button missing")
	}
}

func TestBuildDispatchConfirmation_DisplaysContext(t *testing.T) {
	// given — values that should surface verbatim (after safeTrunc) in the section text
	in := DispatchConfirmation{
		Role:           "paintress",
		Text:           "fix M-42",
		RequesterID:    "U0123ABCD",
		IdempotencyKey: "abcd1234",
		ApproveValue:   "gz:x",
		DenyValue:      "gz:x",
	}

	// when
	payload := BuildDispatchConfirmation(in)

	// then — find the section block and confirm the rendered text mentions each piece
	var sectionText string
	for _, b := range payload.Blocks {
		if b.Type == "section" && b.Text != nil {
			sectionText = b.Text.Text
			break
		}
	}
	if sectionText == "" {
		t.Fatal("expected a section block with rendered text")
	}
	for _, want := range []string{"paintress", "fix M-42", "U0123ABCD", "abcd1234"} {
		if !strings.Contains(sectionText, want) {
			t.Errorf("section text missing %q; got:\n%s", want, sectionText)
		}
	}
}

func TestBuildDispatchConfirmation_ApproveRequiresConfirmDialog(t *testing.T) {
	// given
	in := DispatchConfirmation{Role: "paintress", Text: "x", ApproveValue: "gz:x", DenyValue: "gz:y"}

	// when
	payload := BuildDispatchConfirmation(in)

	// then — the approve button must carry a confirm dialog so a misclick still
	// shows the operator the destructive action before it executes (F-5)
	for _, b := range payload.Blocks {
		for _, el := range b.Elements {
			if el.ActionID == "dispatch_approve" {
				if el.Confirm == nil {
					t.Errorf("dispatch_approve button missing confirm dialog (F-5 guard)")
				}
				return
			}
		}
	}
	t.Fatal("dispatch_approve button not found")
}
