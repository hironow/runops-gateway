package domain_test

import (
	"errors"
	"testing"

	"github.com/hironow/runops-gateway/internal/core/domain"
)

// Phonewave deny is the strictest invariant in the broker grant matrix
// (plan v8 §5.3 / codex review v2 #4): the broker must NEVER mint a
// GitHub installation token for tool=phonewave because phonewave is a
// transport-only D-Mail courier that has no business touching GitHub.
// Returning a "no-permission" token for phonewave would soften the
// access boundary; instead the broker rejects with ErrToolNotPermitted
// for every caller type.
func TestGrantPolicy_PhonewaveDeniedForAllCallers(t *testing.T) {
	policy := domain.DefaultGrantPolicy()
	for _, caller := range domain.AllCallerTypes() {
		err := policy.IsAllowed(caller, domain.ToolPhonewave)
		if err == nil {
			t.Errorf("IsAllowed(%s, phonewave) want error, got nil", caller)
			continue
		}
		if !errors.Is(err, domain.ErrToolNotPermitted) {
			t.Errorf("IsAllowed(%s, phonewave) want ErrToolNotPermitted, got %v", caller, err)
		}
	}
}

// PermissionsFor pins the per-tool repository permission scope from
// plan v8 §5.3. The broker mints tokens with EXACTLY these permissions
// — no more, no less. paintress is the only tool with write access.
func TestGrantPolicy_PermissionsForAllowedTools(t *testing.T) {
	policy := domain.DefaultGrantPolicy()
	cases := map[domain.Tool]domain.RepositoryPermissions{
		domain.ToolPaintress: {Contents: domain.PermWrite, PullRequests: domain.PermWrite},
		domain.ToolSightjack: {Contents: domain.PermRead},
		domain.ToolAmadeus:   {Contents: domain.PermRead, PullRequests: domain.PermRead},
		domain.ToolDominator: {Contents: domain.PermRead},
	}
	for tool, want := range cases {
		got, err := policy.PermissionsFor(tool)
		if err != nil {
			t.Errorf("PermissionsFor(%s) want nil error, got %v", tool, err)
			continue
		}
		if got != want {
			t.Errorf("PermissionsFor(%s) got %+v, want %+v", tool, got, want)
		}
	}
}

// PermissionsFor must also reject phonewave — the deny applies BEFORE
// any permission lookup, otherwise a caller could route around
// IsAllowed by calling PermissionsFor directly and inferring scope.
func TestGrantPolicy_PermissionsForPhonewaveRejected(t *testing.T) {
	policy := domain.DefaultGrantPolicy()
	_, err := policy.PermissionsFor(domain.ToolPhonewave)
	if !errors.Is(err, domain.ErrToolNotPermitted) {
		t.Errorf("PermissionsFor(phonewave) want ErrToolNotPermitted, got %v", err)
	}
}

// ValidateBrokerRequest enforces plan v8 §5.4 request schema lockdown:
// callers may ONLY send project_id / tool / session_id. Any other
// known caller-supplied field (repo / permissions / installation_id /
// actor_type) is a privilege-escalation attempt and the broker MUST
// return ErrRequestSchemaViolation so it surfaces in audit logs as a
// 403 attempt — not 400 (which would imply user error).
func TestValidateBrokerRequest_RejectsCallerSuppliedEscalationFields(t *testing.T) {
	cases := map[string]map[string]any{
		"caller-supplied repo":             {"project_id": "foo", "tool": "paintress", "repo": "evil/other"},
		"caller-supplied repository":       {"project_id": "foo", "tool": "paintress", "repository": "evil/other"},
		"caller-supplied repositories":     {"project_id": "foo", "tool": "paintress", "repositories": []string{"a", "b"}},
		"caller-supplied permissions":      {"project_id": "foo", "tool": "paintress", "permissions": map[string]string{"contents": "write"}},
		"caller-supplied installation_id":  {"project_id": "foo", "tool": "paintress", "installation_id": 123},
		"caller-supplied actor_type":       {"project_id": "foo", "tool": "paintress", "actor_type": "human-operator"},
		"caller-supplied actor.user_email": {"project_id": "foo", "tool": "paintress", "actor": map[string]string{"user_email": "x@y"}},
	}
	for name, raw := range cases {
		err := domain.ValidateBrokerRequest(raw)
		if !errors.Is(err, domain.ErrRequestSchemaViolation) {
			t.Errorf("[%s] ValidateBrokerRequest want ErrRequestSchemaViolation, got %v", name, err)
		}
	}
}

// Unknown fields are 400 (ErrUnknownRequestField), not 403 — the
// plan distinguishes "unknown field = caller bug" from "known
// escalation field = security attempt".
func TestValidateBrokerRequest_RejectsUnknownFieldsAs400(t *testing.T) {
	raw := map[string]any{
		"project_id":         "foo",
		"tool":               "paintress",
		"unknown_field_name": "anything",
	}
	err := domain.ValidateBrokerRequest(raw)
	if !errors.Is(err, domain.ErrUnknownRequestField) {
		t.Errorf("ValidateBrokerRequest unknown field want ErrUnknownRequestField, got %v", err)
	}
}

// Minimal valid request shapes pass.
func TestValidateBrokerRequest_AcceptsValid(t *testing.T) {
	cases := map[string]map[string]any{
		"minimal":         {"project_id": "foo", "tool": "paintress"},
		"with session_id": {"project_id": "foo", "tool": "sightjack", "session_id": "uuid-1234"},
	}
	for name, raw := range cases {
		if err := domain.ValidateBrokerRequest(raw); err != nil {
			t.Errorf("[%s] ValidateBrokerRequest want nil, got %v", name, err)
		}
	}
}
