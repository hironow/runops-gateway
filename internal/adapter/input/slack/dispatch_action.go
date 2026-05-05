package slack

import (
	"encoding/json"
)

// dispatchActionValue is the payload encoded into Slack's button value when the
// /slack/command handler asks the operator to confirm an /agent dispatch. It
// is intentionally separate from actionValue (which carries Phase 0 ChatOps
// approval data) because the two flows have different fields and lifecycles.
//
// The wire format is JSON, then compressed via output/slack.CompressButtonValue
// (gzip + base64url, "gz:" prefix) — same transport as actionValue.
type dispatchActionValue struct {
	Role           string `json:"role"`
	Text           string `json:"text"`
	RequesterID    string `json:"requester_id"`
	IdempotencyKey string `json:"idempotency_key"`
	IssuedAt       int64  `json:"issued_at"`
}

// marshalDispatchActionValue returns the JSON bytes ready to be passed to
// CompressButtonValue. Kept as a small helper so the encoding path is a single
// source for the command handler and tests.
func marshalDispatchActionValue(v dispatchActionValue) ([]byte, error) {
	return json.Marshal(v)
}

// parseDispatchActionValue decodes a Slack button value (raw JSON or
// "gz:"-prefixed compressed form) into a dispatchActionValue. Counterpart of
// the encode path used by the command handler when it returns the confirmation
// Block Kit.
func parseDispatchActionValue(s string) (dispatchActionValue, error) {
	var dv dispatchActionValue
	data, err := decodeButtonValue(s)
	if err != nil {
		return dv, err
	}
	if err := json.Unmarshal(data, &dv); err != nil {
		return dv, err
	}
	return dv, nil
}
