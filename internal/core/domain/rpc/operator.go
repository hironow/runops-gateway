package rpc

import "errors"

// ActorType identifies the kind of caller behind an authenticated request.
//
// Per ADR 0040 §identity contract step 3, an admin token is human-operator-bound:
// AI agent paths must not flow through the multi-token registry. The constant
// is exposed so producers across the codebase can attach `actor_type` consistently.
type ActorType string

const (
	// ActorTypeHumanOperator is the only ActorType produced by the multi-token
	// admin registry (per ADR 0040 §identity contract).
	ActorTypeHumanOperator ActorType = "human-operator"
)

// Operator is the immutable identity carried after a successful Authorization
// header lookup against the multi-token admin registry. It flows through
// context.Context so admin method handlers can populate audit metadata
// (effective_requester_id) without re-running the auth path.
//
// The Email is for log/audit only; do NOT use it for identity matching
// (operator_id is the canonical key, aligned with the Slack user_id namespace
// for ADR 0035 4-eyes invariant).
type Operator struct {
	OperatorID string
	Email      string
	ActorType  ActorType
}

// NewOperator validates the input and returns a populated Operator. Returns
// an error when invariants are violated (e.g., empty operator_id).
//
// ActorType is fixed to ActorTypeHumanOperator per ADR 0040; callers cannot
// override it.
func NewOperator(operatorID, email string) (Operator, error) {
	if operatorID == "" {
		return Operator{}, errors.New("operator_id must not be empty")
	}
	return Operator{
		OperatorID: operatorID,
		Email:      email,
		ActorType:  ActorTypeHumanOperator,
	}, nil
}

// IsZero reports whether the receiver is the zero value (= unauthenticated /
// no operator carried). Useful for context-derived checks where the lookup
// returned no match.
func (o Operator) IsZero() bool {
	return o.OperatorID == "" && o.Email == "" && o.ActorType == ""
}
