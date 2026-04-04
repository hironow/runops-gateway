package slack

import (
	"strings"
	"testing"
)

func TestValidate_EmptyPayload_NoTextNoBlocks(t *testing.T) {
	p := SlackPayload{ReplaceOriginal: true}
	if err := p.Validate(); err == nil {
		t.Error("expected error for payload with neither text nor blocks")
	}
}

func TestValidate_TextOnly_OK(t *testing.T) {
	p := TextPayload("hello")
	if err := p.Validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_BlocksOnly_OK(t *testing.T) {
	p := ReplacePayload(SectionBlock("hello"))
	if err := p.Validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_DuplicateActionID_Error(t *testing.T) {
	// Two buttons with same action_id in one actions block
	btn1 := NewButton("approve", "Next", "v1", "primary")
	btn2 := NewButton("approve", "Stop", "v2", "danger")
	p := ReplacePayload(SectionBlock("test"), ActionsBlock(btn1, btn2))

	err := p.Validate()
	if err == nil {
		t.Fatal("expected error for duplicate action_id")
	}
	if !strings.Contains(err.Error(), "approve") {
		t.Errorf("error should mention the duplicate action_id, got: %v", err)
	}
}

func TestValidate_UniqueActionIDs_OK(t *testing.T) {
	btn1 := NewButton("approve", "Next", "v1", "primary")
	btn2 := NewButton("approve_rollback", "Stop", "v2", "danger")
	p := ReplacePayload(SectionBlock("test"), ActionsBlock(btn1, btn2))

	if err := p.Validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_ButtonValueTooLong_Error(t *testing.T) {
	longValue := strings.Repeat("x", maxButtonValue+1)
	btn := NewButton("act", "Label", longValue, "primary")
	p := ReplacePayload(ActionsBlock(btn))

	err := p.Validate()
	if err == nil {
		t.Fatal("expected error for button value exceeding limit")
	}
	if !strings.Contains(err.Error(), "2000") {
		t.Errorf("error should mention limit, got: %v", err)
	}
}

func TestValidate_ButtonLabelTooLong_Error(t *testing.T) {
	longLabel := strings.Repeat("あ", maxButtonLabel+1)
	btn := NewButton("act", longLabel, "v", "primary")
	p := ReplacePayload(ActionsBlock(btn))

	err := p.Validate()
	if err == nil {
		t.Fatal("expected error for button label exceeding limit")
	}
}

func TestValidate_SectionTextTooLong_Error(t *testing.T) {
	longText := strings.Repeat("a", maxSectionText+1)
	p := ReplacePayload(SectionBlock(longText))

	err := p.Validate()
	if err == nil {
		t.Fatal("expected error for section text exceeding limit")
	}
}

func TestValidate_HeaderTextTooLong_Error(t *testing.T) {
	longText := strings.Repeat("a", maxHeaderText+1)
	p := ReplacePayload(HeaderBlock(longText))

	err := p.Validate()
	if err == nil {
		t.Fatal("expected error for header text exceeding limit")
	}
}

func TestValidate_EmptyButton_MissingActionID(t *testing.T) {
	btn := Button{Type: "button", Text: *PlainText("click"), Value: "v"}
	p := ReplacePayload(ActionsBlock(btn))

	err := p.Validate()
	if err == nil {
		t.Fatal("expected error for button without action_id")
	}
}

func TestValidate_EphemeralPayload_OK(t *testing.T) {
	p := EphemeralPayload("hello")
	if err := p.Validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}
