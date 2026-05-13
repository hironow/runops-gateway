package slack

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// ApprovalKind identifies which approval flow a button click belongs to.
// Producers populate it via DMail.Metadata["kind"]; handleApprovalAction
// branches on it to route Phase 4a convergence vs ADR 0040 §B-5 admin
// mutation flows to their respective applicators.
//
// Empty value defaults to "convergence" so all Phase 4a producers keep
// working without changes.
type ApprovalKind string

const (
	// ApprovalKindConvergence is the Phase 4a HIGH severity convergence
	// approval (= ADR 0019). The default when av.Kind is empty.
	ApprovalKindConvergence ApprovalKind = "convergence"
	// ApprovalKindAdminMutation is the ADR 0040 §B-5 admin-mutation
	// approval. ParentIdempotencyKey carries the PendingApproval key,
	// OriginalRequesterID carries the EffectiveRequesterID (= operator
	// id from the multi-admin-token registry, same Slack user_id
	// namespace per §identity contract).
	ApprovalKindAdminMutation ApprovalKind = "admin_mutation"
)

// approvalActionValue is the payload encoded into the Approve / Deny buttons
// of an approval request. Phase 4a (ADR 0019) HIGH severity convergence and
// ADR 0040 §B-5 admin-mutation flows share the same button shape, with the
// Kind field disambiguating which applicator routes the click.
type approvalActionValue struct {
	// Kind discriminates the approval flow. Empty value is interpreted
	// as ApprovalKindConvergence for backwards compatibility with
	// pre-§B-5 producers.
	Kind                 ApprovalKind `json:"kind,omitempty"`
	ParentIdempotencyKey string       `json:"parent_idempotency_key"`
	OriginalRequesterID  string       `json:"original_requester_id"` // for the 4-eyes guard
	Source               string       `json:"source"`                // 元 D-Mail の source (e.g. "amadeus")
	Target               string       `json:"target"`                // 元 D-Mail の target (e.g. "sightjack")
	BodyDigest           string       `json:"body_digest"`           // SHA-256 prefix of original body — tamper guard
	IssuedAt             int64        `json:"issued_at"`
	// RequesterActorType is the actor type of the original requester
	// (per ADR 0036 §Carry point 2). Empty value is treated as
	// CallerHumanOperator during the migration window. Producers SHOULD
	// emit a canonical CallerType string ("human-operator" / "ai-agent" /
	// "gateway-service" / "workspace-daemon"). Unknown non-empty values
	// are rejected by handleApprovalAction.
	RequesterActorType string `json:"requester_actor_type,omitempty"`
	// RequesterActorSource carries the gateway-internal classification
	// derived at button-build time (per ADR 0037 §Axis 4). One of:
	// "broker_verified" / "env_attested" / "unknown" / "spoofed_broker".
	// Empty value is treated as "unknown" by handleApprovalAction.
	RequesterActorSource string `json:"requester_actor_source,omitempty"`
	// InitiatingActorType is the distal actor when RequesterActorType
	// is workspace-daemon (per ADR 0037 §Axis 3). REQUIRED for HIGH
	// severity Phase 4a approvals when RequesterActorType is
	// workspace-daemon; empty otherwise.
	InitiatingActorType string `json:"initiating_actor_type,omitempty"`
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
