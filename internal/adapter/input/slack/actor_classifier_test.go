package slack

import (
	"testing"

	"github.com/hironow/runops-gateway/internal/core/domain"
)

// TestClassifyApproverActorType covers the ADR 0035 §Layer 3 contract:
// a Slack user.id is classified as CallerAIAgent iff it appears in the
// aiBotIDs allow-list (typically sourced from SLACK_AI_AGENT_BOT_USER_IDS).
// Empty list => every user is human (= safe default; rule is a no-op).
func TestClassifyApproverActorType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		userID   string
		aiBotIDs []string
		want     domain.CallerType
	}{
		{name: "empty list classifies any user as human", userID: "U_HUMAN_123", aiBotIDs: nil, want: domain.CallerHumanOperator},
		{name: "match in single-entry list returns AI agent", userID: "B_BOT_AGENT_1", aiBotIDs: []string{"B_BOT_AGENT_1"}, want: domain.CallerAIAgent},
		{name: "non-match against single-entry list returns human", userID: "U_HUMAN_123", aiBotIDs: []string{"B_BOT_AGENT_1"}, want: domain.CallerHumanOperator},
		{name: "match against multi-entry list returns AI agent", userID: "B_BOT_AGENT_2", aiBotIDs: []string{"B_BOT_AGENT_1", "B_BOT_AGENT_2", "B_BOT_AGENT_3"}, want: domain.CallerAIAgent},
		{name: "non-match against multi-entry list returns human", userID: "U_HUMAN_999", aiBotIDs: []string{"B_BOT_AGENT_1", "B_BOT_AGENT_2"}, want: domain.CallerHumanOperator},
		{name: "empty userID returns human (defensive default)", userID: "", aiBotIDs: []string{"B_BOT_AGENT_1"}, want: domain.CallerHumanOperator},
		{name: "whitespace-padded list entry still matches", userID: "B_BOT_AGENT_1", aiBotIDs: []string{"  B_BOT_AGENT_1  "}, want: domain.CallerAIAgent},
		{name: "exact-match required (no substring match)", userID: "B_BOT_AGENT_1_SUFFIX", aiBotIDs: []string{"B_BOT_AGENT_1"}, want: domain.CallerHumanOperator},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ClassifyApproverActorType(tt.userID, tt.aiBotIDs)
			if got != tt.want {
				t.Fatalf("ClassifyApproverActorType(%q, %v) = %q, want %q", tt.userID, tt.aiBotIDs, got, tt.want)
			}
		})
	}
}

// TestParseAIAgentBotUserIDs verifies CSV parsing of the env var, with
// trimming and empty-entry filtering.
func TestParseAIAgentBotUserIDs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		csv  string
		want []string
	}{
		{name: "empty string returns nil slice", csv: "", want: nil},
		{name: "single entry returns one-element slice", csv: "B_BOT_1", want: []string{"B_BOT_1"}},
		{name: "two entries split on comma", csv: "B_BOT_1,B_BOT_2", want: []string{"B_BOT_1", "B_BOT_2"}},
		{name: "leading and trailing whitespace trimmed per entry", csv: "  B_BOT_1 , B_BOT_2  ", want: []string{"B_BOT_1", "B_BOT_2"}},
		{name: "empty-after-trim entries dropped", csv: "B_BOT_1,,  ,B_BOT_2", want: []string{"B_BOT_1", "B_BOT_2"}},
		{name: "all-empty input returns empty slice", csv: "  ,  ,  ", want: []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ParseAIAgentBotUserIDs(tt.csv)
			if len(got) != len(tt.want) {
				t.Fatalf("ParseAIAgentBotUserIDs(%q) length = %d, want %d (got=%v want=%v)", tt.csv, len(got), len(tt.want), got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("ParseAIAgentBotUserIDs(%q)[%d] = %q, want %q", tt.csv, i, got[i], tt.want[i])
				}
			}
		})
	}
}
