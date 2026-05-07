package domain

import "fmt"

// AgentRole identifies one of the five-pillar AI agents that can receive a dispatch.
// Phonewave is intentionally absent — it is the courier, not a target role.
type AgentRole string

const (
	AgentRolePaintress AgentRole = "paintress"
	AgentRoleSightjack AgentRole = "sightjack"
	AgentRoleAmadeus   AgentRole = "amadeus"
	AgentRoleDominator AgentRole = "dominator"
)

// ParseAgentRole returns the canonical AgentRole for s or an error if s is not
// one of the four supported lowercase role names.
func ParseAgentRole(s string) (AgentRole, error) {
	switch AgentRole(s) {
	case AgentRolePaintress, AgentRoleSightjack, AgentRoleAmadeus, AgentRoleDominator:
		return AgentRole(s), nil
	default:
		return "", fmt.Errorf("unknown agent role: %q (want paintress|sightjack|amadeus|dominator)", s)
	}
}

// DispatchRequest carries a Slack /agent or CLI dispatch request through the use
// case layer. It is intentionally separate from ApprovalRequest because dispatch
// has different semantics (immediate execution vs gated approval).
type DispatchRequest struct {
	// Role is the target agent (paintress / sightjack / amadeus / dominator).
	Role AgentRole
	// Text is the free-form instruction passed by the requester.
	Text string
	// RequesterID is the Slack user ID or CLI email of the human submitting the dispatch.
	RequesterID string
	// IdempotencyKey identifies a single dispatch attempt; used for dedup across retries.
	IdempotencyKey string
	// IssuedAt is the Unix timestamp at which the request was created.
	IssuedAt int64
	// SlackChannelID is the Slack channel.id of the originating /agent
	// invocation. Phase 3 carries this through Pub/Sub metadata so the
	// outbound subscriber can chat.postMessage thread-replies into the
	// right channel without consulting external state.
	// Empty for CLI dispatches.
	SlackChannelID string
	// SlackThreadTS is the Slack message.ts to thread reply onto. Empty
	// for CLI dispatches.
	SlackThreadTS string
	// ProjectID is the multiplex project_id (issue #0008/#0009/#0011).
	// Empty when --project was not specified; non-empty when the Slack
	// command supplied a registered project. See ADR 0027.
	ProjectID string
}

// OperationKey returns a canonical deduplication key for a DispatchRequest.
// Phase 1: in-process MemoryStore uses this; Phase 2+ Pub/Sub bridge will reuse
// IdempotencyKey directly as the message attribute.
func (r DispatchRequest) OperationKey() string {
	return fmt.Sprintf("dispatch/%s/%s/%s/%d", r.Role, r.RequesterID, r.IdempotencyKey, r.IssuedAt)
}
