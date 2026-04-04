// Package slack provides typed structures for Slack Block Kit payloads.
// Using concrete types instead of map[string]any prevents structural bugs
// (e.g. double-wrapping replace_original, missing required fields).
package slack

// SlackPayload is the top-level JSON body sent to Slack's response_url.
type SlackPayload struct {
	ReplaceOriginal bool    `json:"replace_original"`
	ResponseType    string  `json:"response_type,omitempty"`
	Text            string  `json:"text,omitempty"`
	Blocks          []Block `json:"blocks,omitempty"`
}

// BlockType enumerates the supported Slack block types.
type BlockType string

const (
	BlockTypeSection BlockType = "section"
	BlockTypeActions BlockType = "actions"
	BlockTypeHeader  BlockType = "header"
	BlockTypeDivider BlockType = "divider"
)

// Block represents a single Block Kit block.
// Not all fields apply to every block type; unused fields are omitted via omitempty.
type Block struct {
	Type      BlockType  `json:"type"`
	Text      *TextObj   `json:"text,omitempty"`
	Accessory *Accessory `json:"accessory,omitempty"`
	Elements  []Button   `json:"elements,omitempty"`
}

// TextObj is a Slack text object (mrkdwn or plain_text).
type TextObj struct {
	Type  string `json:"type"`
	Text  string `json:"text"`
	Emoji bool   `json:"emoji,omitempty"`
}

// Accessory is an image accessory attached to a section block.
type Accessory struct {
	Type     string `json:"type"`
	ImageURL string `json:"image_url"`
	AltText  string `json:"alt_text"`
}

// Button is a button element within an actions block.
type Button struct {
	Type     string         `json:"type"`
	ActionID string         `json:"action_id"`
	Style    string         `json:"style,omitempty"`
	Text     TextObj        `json:"text"`
	Value    string         `json:"value"`
	Confirm  *ConfirmDialog `json:"confirm,omitempty"`
}

// ConfirmDialog is a confirmation popup shown when a button is clicked.
type ConfirmDialog struct {
	Title   TextObj `json:"title"`
	Text    TextObj `json:"text"`
	Confirm TextObj `json:"confirm"`
	Deny    TextObj `json:"deny"`
}

// --- Constructors ---

// Mrkdwn creates a mrkdwn text object.
func Mrkdwn(text string) *TextObj {
	return &TextObj{Type: "mrkdwn", Text: text}
}

// PlainText creates a plain_text object with emoji enabled.
func PlainText(text string) *TextObj {
	return &TextObj{Type: "plain_text", Text: text, Emoji: true}
}

// SectionBlock creates a section block with mrkdwn text.
func SectionBlock(text string) Block {
	return Block{Type: BlockTypeSection, Text: Mrkdwn(text)}
}

// SectionBlockWithAccessory creates a section block with mrkdwn text and an image accessory.
func SectionBlockWithAccessory(text, imageURL, altText string) Block {
	return Block{
		Type: BlockTypeSection,
		Text: Mrkdwn(text),
		Accessory: &Accessory{
			Type:     "image",
			ImageURL: imageURL,
			AltText:  altText,
		},
	}
}

// HeaderBlock creates a header block with plain text.
func HeaderBlock(text string) Block {
	return Block{Type: BlockTypeHeader, Text: PlainText(text)}
}

// DividerBlock creates a divider block.
func DividerBlock() Block {
	return Block{Type: BlockTypeDivider}
}

// ActionsBlock creates an actions block with the given buttons.
func ActionsBlock(buttons ...Button) Block {
	return Block{Type: BlockTypeActions, Elements: buttons}
}

// NewButton creates a button element.
func NewButton(actionID, label, value, style string) Button {
	return Button{
		Type:     "button",
		ActionID: actionID,
		Style:    style,
		Text:     *PlainText(label),
		Value:    value,
	}
}

// WithConfirm attaches a confirmation dialog to a button.
func (b Button) WithConfirm(title, text, confirm, deny string) Button {
	b.Confirm = &ConfirmDialog{
		Title:   TextObj{Type: "plain_text", Text: title},
		Text:    *Mrkdwn(text),
		Confirm: TextObj{Type: "plain_text", Text: confirm},
		Deny:    TextObj{Type: "plain_text", Text: deny},
	}
	return b
}

// ReplacePayload creates a payload that replaces the original message with blocks.
func ReplacePayload(blocks ...Block) SlackPayload {
	return SlackPayload{
		ReplaceOriginal: true,
		Blocks:          blocks,
	}
}

// TextPayload creates a payload that replaces the original message with plain text.
func TextPayload(text string) SlackPayload {
	return SlackPayload{
		ReplaceOriginal: true,
		Text:            text,
	}
}

// EphemeralPayload creates an ephemeral message payload.
func EphemeralPayload(text string) SlackPayload {
	return SlackPayload{
		ResponseType:    "ephemeral",
		ReplaceOriginal: false,
		Text:            text,
	}
}
