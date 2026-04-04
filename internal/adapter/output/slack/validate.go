package slack

import "fmt"

// Validate checks the payload against Slack Block Kit constraints before sending.
// Returns an error describing the first violation found.
func (p SlackPayload) Validate() error {
	if p.Text == "" && len(p.Blocks) == 0 {
		return fmt.Errorf("slack validate: payload must have text or blocks")
	}

	for i, block := range p.Blocks {
		if err := validateBlock(block, i); err != nil {
			return err
		}
	}
	return nil
}

func validateBlock(b Block, idx int) error {
	switch b.Type {
	case BlockTypeHeader:
		if b.Text != nil && len([]rune(b.Text.Text)) > maxHeaderText {
			return fmt.Errorf("slack validate: block[%d] header text exceeds %d runes", idx, maxHeaderText)
		}
	case BlockTypeSection:
		if b.Text != nil && len([]rune(b.Text.Text)) > maxSectionText {
			return fmt.Errorf("slack validate: block[%d] section text exceeds %d runes", idx, maxSectionText)
		}
	case BlockTypeActions:
		return validateActions(b.Elements, idx)
	case BlockTypeDivider:
		// no validation needed
	}
	return nil
}

func validateActions(buttons []Button, blockIdx int) error {
	seen := make(map[string]bool, len(buttons))
	for i, btn := range buttons {
		if btn.ActionID == "" {
			return fmt.Errorf("slack validate: block[%d].elements[%d] button missing action_id", blockIdx, i)
		}
		if seen[btn.ActionID] {
			return fmt.Errorf("slack validate: block[%d] duplicate action_id %q — Slack requires unique action_ids per actions block", blockIdx, btn.ActionID)
		}
		seen[btn.ActionID] = true

		if len(btn.Value) > maxButtonValue {
			return fmt.Errorf("slack validate: block[%d].elements[%d] button value (%d chars) exceeds limit (%d)", blockIdx, i, len(btn.Value), maxButtonValue)
		}
		if len([]rune(btn.Text.Text)) > maxButtonLabel {
			return fmt.Errorf("slack validate: block[%d].elements[%d] button label (%d runes) exceeds limit (%d)", blockIdx, i, len([]rune(btn.Text.Text)), maxButtonLabel)
		}
	}
	return nil
}
