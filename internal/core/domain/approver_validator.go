package domain

import "errors"

// ErrAIAgentCannotApproveAIAgent is returned by ValidateApproverPermitted
// when both the original requester and the approver are AI agents.
// Pins the structural rule of ADR 0035 (refs#0011) at the domain layer.
var ErrAIAgentCannotApproveAIAgent = errors.New("ai agent cannot approve ai agent")

// ValidateApproverPermitted enforces the ADR 0035 invariant: an AI agent
// cannot approve another AI agent's convergence request. Empty CallerType
// (zero value) is treated as CallerHumanOperator to preserve backwards
// compatibility with construction sites that pre-date RequesterActorType.
//
// All other combinations (human-vs-AI, gateway-vs-AI, etc.) are permitted
// at the domain layer; further restrictions are layered on by the
// use-case (auth.IsAuthorized) and the Slack inbound adapter
// (SLACK_AI_AGENT_BOT_USER_IDS classification).
func ValidateApproverPermitted(req ApprovalRequest, approverType CallerType) error {
	requester := req.RequesterActorType
	if requester == "" {
		requester = CallerHumanOperator
	}
	if approverType == "" {
		approverType = CallerHumanOperator
	}
	if requester == CallerAIAgent && approverType == CallerAIAgent {
		return ErrAIAgentCannotApproveAIAgent
	}
	return nil
}
