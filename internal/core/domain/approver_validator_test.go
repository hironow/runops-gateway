package domain

import (
	"errors"
	"testing"
)

// TestValidateApproverPermitted exercises the ADR 0035 structural rule:
// AI agent cannot approve AI agent. The matrix covers all combinations
// of caller types for both requester and approver, including zero
// value (empty string) which is treated as CallerHumanOperator.
func TestValidateApproverPermitted(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		requester    CallerType
		approver     CallerType
		wantSentinel bool
	}{
		{name: "human-requester human-approver permitted", requester: CallerHumanOperator, approver: CallerHumanOperator, wantSentinel: false},
		{name: "human-requester ai-approver permitted", requester: CallerHumanOperator, approver: CallerAIAgent, wantSentinel: false},
		{name: "human-requester gateway-approver permitted", requester: CallerHumanOperator, approver: CallerGatewayService, wantSentinel: false},
		{name: "human-requester workspace-approver permitted", requester: CallerHumanOperator, approver: CallerWorkspaceDaemon, wantSentinel: false},

		{name: "ai-requester human-approver permitted", requester: CallerAIAgent, approver: CallerHumanOperator, wantSentinel: false},
		{name: "ai-requester ai-approver REJECTED", requester: CallerAIAgent, approver: CallerAIAgent, wantSentinel: true},
		{name: "ai-requester gateway-approver permitted", requester: CallerAIAgent, approver: CallerGatewayService, wantSentinel: false},
		{name: "ai-requester workspace-approver permitted", requester: CallerAIAgent, approver: CallerWorkspaceDaemon, wantSentinel: false},

		{name: "gateway-requester ai-approver permitted", requester: CallerGatewayService, approver: CallerAIAgent, wantSentinel: false},
		{name: "workspace-requester ai-approver permitted", requester: CallerWorkspaceDaemon, approver: CallerAIAgent, wantSentinel: false},

		{name: "zero-requester ai-approver permitted (zero=human)", requester: "", approver: CallerAIAgent, wantSentinel: false},
		{name: "ai-requester zero-approver permitted (zero=human)", requester: CallerAIAgent, approver: "", wantSentinel: false},
		{name: "zero-requester zero-approver permitted (zero=human)", requester: "", approver: "", wantSentinel: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := ApprovalRequest{RequesterActorType: tt.requester}
			err := ValidateApproverPermitted(req, tt.approver)

			if tt.wantSentinel {
				if !errors.Is(err, ErrAIAgentCannotApproveAIAgent) {
					t.Fatalf("expected ErrAIAgentCannotApproveAIAgent, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("expected nil error, got %v", err)
			}
		})
	}
}

// TestErrAIAgentCannotApproveAIAgent_IsExported confirms the sentinel
// is exported and matchable via errors.Is so use-case layers can wrap
// it without losing the comparison signal.
func TestErrAIAgentCannotApproveAIAgent_IsExported(t *testing.T) {
	t.Parallel()

	if ErrAIAgentCannotApproveAIAgent == nil {
		t.Fatal("ErrAIAgentCannotApproveAIAgent must be a non-nil sentinel")
	}
	wrapped := errors.New("wrapped: " + ErrAIAgentCannotApproveAIAgent.Error())
	if errors.Is(wrapped, ErrAIAgentCannotApproveAIAgent) {
		t.Fatal("plain wrap via Errorf without %w must not match Is — guards against accidental string-based identity")
	}
}
