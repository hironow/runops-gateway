package slack

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// approvalActionValue is the payload encoded into the Approve / Deny buttons
// of a HIGH severity convergence approval request (ADR 0019). Distinct from
// dispatchActionValue: the audit shape is different (parent + 4-eyes guard
// fields) and the lifecycle is different (one-time approval rather than the
// dispatch confirmation pattern).
type approvalActionValue struct {
	ParentIdempotencyKey string `json:"parent_idempotency_key"`
	OriginalRequesterID  string `json:"original_requester_id"` // for the 4-eyes guard
	Source               string `json:"source"`                // 元 D-Mail の source (e.g. "amadeus")
	Target               string `json:"target"`                // 元 D-Mail の target (e.g. "sightjack")
	BodyDigest           string `json:"body_digest"`           // SHA-256 prefix of original body — tamper guard
	IssuedAt             int64  `json:"issued_at"`
}

// ApprovalBodyDigest returns the digest used in approvalActionValue.BodyDigest
// (SHA-256 of body, first 16 hex chars). Exported so DispatchResultHandler can
// produce matching values when it builds the buttons.
func ApprovalBodyDigest(body string) string {
	h := sha256.Sum256([]byte(body))
	return hex.EncodeToString(h[:])[:16]
}

func marshalApprovalActionValue(v approvalActionValue) ([]byte, error) {
	return json.Marshal(v)
}

func parseApprovalActionValue(s string) (approvalActionValue, error) {
	var av approvalActionValue
	data, err := decodeButtonValue(s)
	if err != nil {
		return av, err
	}
	if err := json.Unmarshal(data, &av); err != nil {
		return av, err
	}
	return av, nil
}
