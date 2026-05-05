package slack

import (
	"strings"
	"testing"

	outputslack "github.com/hironow/runops-gateway/internal/adapter/output/slack"
)

func TestDispatchActionValue_RoundTrip(t *testing.T) {
	// given — a representative dispatch payload
	original := dispatchActionValue{
		Role:           "paintress",
		Text:           "fix M-42",
		RequesterID:    "U0123ABCD",
		IdempotencyKey: "ab12cd34",
		IssuedAt:       1700000000,
	}

	// when — marshal -> compress -> parse
	raw, err := marshalDispatchActionValue(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	encoded := outputslack.CompressButtonValue(string(raw))
	decoded, err := parseDispatchActionValue(encoded)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	// then — round-trip preserves every field
	if decoded != original {
		t.Errorf("round-trip drifted: got %+v want %+v", decoded, original)
	}
	if !strings.HasPrefix(encoded, "gz:") {
		t.Errorf("encoded form should use gz: prefix, got %q", encoded[:min(8, len(encoded))])
	}
}

func TestParseDispatchActionValue_AcceptsRawJSONLegacy(t *testing.T) {
	// given — a raw JSON body without the "gz:" prefix (legacy / test convenience)
	raw := `{"role":"sightjack","text":"scan","requester_id":"U999","idempotency_key":"k","issued_at":1}`

	// when
	dv, err := parseDispatchActionValue(raw)
	if err != nil {
		t.Fatalf("parse legacy raw json: %v", err)
	}

	// then
	if dv.Role != "sightjack" || dv.Text != "scan" || dv.RequesterID != "U999" {
		t.Errorf("unexpected decoded fields: %+v", dv)
	}
}

func TestParseDispatchActionValue_RejectsGarbage(t *testing.T) {
	if _, err := parseDispatchActionValue("gz:not-base64-data!!!"); err == nil {
		t.Error("expected error for malformed gz: payload, got nil")
	}
	if _, err := parseDispatchActionValue("{not json}"); err == nil {
		t.Error("expected error for malformed JSON, got nil")
	}
}
