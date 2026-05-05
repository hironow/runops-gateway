package slack

import (
	"strings"
	"testing"

	outputslack "github.com/hironow/runops-gateway/internal/adapter/output/slack"
)

func TestApprovalActionValue_RoundTrip(t *testing.T) {
	original := approvalActionValue{
		ParentIdempotencyKey: "parent-123",
		OriginalRequesterID:  "U_ORIG",
		Source:               "amadeus",
		Target:               "sightjack",
		BodyDigest:           "deadbeefcafe1234",
		IssuedAt:             1700000000,
	}
	raw, err := marshalApprovalActionValue(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	encoded := outputslack.CompressButtonValue(string(raw))
	decoded, err := parseApprovalActionValue(encoded)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if decoded != original {
		t.Errorf("round-trip drifted: got %+v want %+v", decoded, original)
	}
	if !strings.HasPrefix(encoded, "gz:") {
		t.Errorf("encoded form should use gz: prefix, got %q", encoded[:8])
	}
}

func TestApprovalBodyDigest_StableAndDistinguishing(t *testing.T) {
	a := ApprovalBodyDigest("hello world")
	b := ApprovalBodyDigest("hello world")
	if a != b {
		t.Errorf("digest must be stable: %q vs %q", a, b)
	}
	c := ApprovalBodyDigest("different")
	if a == c {
		t.Errorf("digest must distinguish bodies: %q (same body)", a)
	}
	if len(a) != 16 {
		t.Errorf("digest should be 16 hex chars, got %d (%q)", len(a), a)
	}
}
