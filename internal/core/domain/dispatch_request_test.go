package domain_test

import (
	"strings"
	"testing"

	"github.com/hironow/runops-gateway/internal/core/domain"
)

func TestAgentRoleConstants(t *testing.T) {
	cases := []struct {
		role domain.AgentRole
		want string
	}{
		{domain.AgentRolePaintress, "paintress"},
		{domain.AgentRoleSightjack, "sightjack"},
		{domain.AgentRoleAmadeus, "amadeus"},
		{domain.AgentRoleDominator, "dominator"},
	}
	for _, tc := range cases {
		if string(tc.role) != tc.want {
			t.Errorf("role=%q, want %q", string(tc.role), tc.want)
		}
	}
}

func TestParseAgentRole_AcceptsKnownRoles(t *testing.T) {
	for _, name := range []string{"paintress", "sightjack", "amadeus", "dominator"} {
		role, err := domain.ParseAgentRole(name)
		if err != nil {
			t.Errorf("ParseAgentRole(%q) returned error: %v", name, err)
		}
		if string(role) != name {
			t.Errorf("ParseAgentRole(%q) = %q", name, role)
		}
	}
}

func TestParseAgentRole_RejectsUnknown(t *testing.T) {
	cases := []string{"", "phonewave", "unknown", "PAINTRESS"}
	for _, name := range cases {
		_, err := domain.ParseAgentRole(name)
		if err == nil {
			t.Errorf("ParseAgentRole(%q) expected error, got nil", name)
		}
	}
}

func TestDispatchRequestZeroValue(t *testing.T) {
	var req domain.DispatchRequest
	if req.Role != "" {
		t.Errorf("expected empty Role, got %q", req.Role)
	}
	if req.IssuedAt != 0 {
		t.Errorf("expected IssuedAt == 0, got %d", req.IssuedAt)
	}
}

func TestDispatchRequestFields(t *testing.T) {
	req := domain.DispatchRequest{
		Role:           domain.AgentRolePaintress,
		Text:           "fix M-42",
		RequesterID:    "U0123ABCD",
		IdempotencyKey: "abc123",
		IssuedAt:       1700000000,
	}
	if req.Role != domain.AgentRolePaintress {
		t.Errorf("Role=%q", req.Role)
	}
	if req.Text != "fix M-42" {
		t.Errorf("Text=%q", req.Text)
	}
	if req.RequesterID != "U0123ABCD" {
		t.Errorf("RequesterID=%q", req.RequesterID)
	}
	if req.IdempotencyKey != "abc123" {
		t.Errorf("IdempotencyKey=%q", req.IdempotencyKey)
	}
}

func TestDispatchRequestOperationKey_StableAndUnique(t *testing.T) {
	a := domain.DispatchRequest{
		Role:           domain.AgentRolePaintress,
		Text:           "fix M-42",
		RequesterID:    "U0123",
		IdempotencyKey: "k1",
		IssuedAt:       1700000000,
	}
	b := a
	if a.OperationKey() != b.OperationKey() {
		t.Errorf("OperationKey not stable: %q vs %q", a.OperationKey(), b.OperationKey())
	}
	c := a
	c.IdempotencyKey = "k2"
	if a.OperationKey() == c.OperationKey() {
		t.Errorf("OperationKey collided across IdempotencyKey: %q", a.OperationKey())
	}
	if !strings.Contains(a.OperationKey(), "paintress") {
		t.Errorf("OperationKey should encode role; got %q", a.OperationKey())
	}
	if !strings.Contains(a.OperationKey(), "k1") {
		t.Errorf("OperationKey should encode idempotency key; got %q", a.OperationKey())
	}
}
