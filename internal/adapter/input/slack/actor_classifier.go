package slack

import (
	"strings"

	"github.com/hironow/runops-gateway/internal/core/domain"
)

// ClassifyApproverActorType decides whether a Slack user.id corresponds
// to an AI agent or a human operator, per ADR 0035 §Layer 3.
//
// The rule is a no-op until at least one bot user.id is enrolled in
// aiBotIDs (typically populated from SLACK_AI_AGENT_BOT_USER_IDS, CSV).
// Empty aiBotIDs => every user is classified as CallerHumanOperator
// (= safe default). Empty userID => CallerHumanOperator (defensive
// default; should not occur in normal Slack interactive payloads).
//
// Match is exact string equality after trimming whitespace from each
// list entry. No prefix or substring matching is applied — operators
// must enroll bot user IDs explicitly to opt in to AI-agent classification.
func ClassifyApproverActorType(userID string, aiBotIDs []string) domain.CallerType {
	if userID == "" {
		return domain.CallerHumanOperator
	}
	for _, id := range aiBotIDs {
		if strings.TrimSpace(id) == userID {
			return domain.CallerAIAgent
		}
	}
	return domain.CallerHumanOperator
}

// ParseAIAgentBotUserIDs parses the SLACK_AI_AGENT_BOT_USER_IDS env var
// (CSV) into a slice. Empty entries (after trim) are dropped. Returned
// slice may be nil when the input is the empty string, signalling
// "AI-vs-AI rule disabled" to ClassifyApproverActorType.
func ParseAIAgentBotUserIDs(csv string) []string {
	if csv == "" {
		return nil
	}
	parts := strings.Split(csv, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
