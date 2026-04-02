package auth

import (
	"testing"

	"github.com/hironow/runops-gateway/internal/core/port"
)

// compile-time interface assertion
var _ port.AuthChecker = (*EnvAuthChecker)(nil)

// newTestChecker creates an EnvAuthChecker with injected dependencies for testing.
func newTestChecker(users []string, expiry int64, nowFn func() int64) *EnvAuthChecker {
	return &EnvAuthChecker{allowedUsers: users, expirySeconds: expiry, now: nowFn}
}

func TestIsAuthorized_AllowedUser(t *testing.T) {
	// given
	c := newTestChecker([]string{"U001", "U002"}, defaultExpirySeconds, nil)

	// when
	result := c.IsAuthorized("U001")

	// then
	if !result {
		t.Error("expected U001 to be authorized")
	}
}

func TestIsAuthorized_UnknownUser(t *testing.T) {
	// given
	c := newTestChecker([]string{"U001", "U002"}, defaultExpirySeconds, nil)

	// when
	result := c.IsAuthorized("U999")

	// then
	if result {
		t.Error("expected U999 to be unauthorized")
	}
}

func TestIsAuthorized_EmptyList(t *testing.T) {
	// given
	c := newTestChecker([]string{}, defaultExpirySeconds, nil)

	// when
	result := c.IsAuthorized("U001")

	// then
	if result {
		t.Error("expected deny by default when allowed list is empty")
	}
}

func TestIsAuthorized_WhitespaceTrimming(t *testing.T) {
	// given
	users := parseAllowedUsers("  U123  , U456 ")
	c := newTestChecker(users, defaultExpirySeconds, nil)

	// when
	result := c.IsAuthorized("U123")

	// then
	if !result {
		t.Error("expected U123 to match after trimming whitespace")
	}
}

func TestIsExpired_WithinWindow(t *testing.T) {
	// given
	now := int64(10000)
	c := newTestChecker(nil, 7200, func() int64 { return now })
	issuedAt := now - 100

	// when
	result := c.IsExpired(issuedAt)

	// then
	if result {
		t.Error("expected not expired when within window")
	}
}

func TestIsExpired_Expired(t *testing.T) {
	// given
	now := int64(10000)
	c := newTestChecker(nil, 7200, func() int64 { return now })
	issuedAt := now - 7201

	// when
	result := c.IsExpired(issuedAt)

	// then
	if !result {
		t.Error("expected expired when past expiry window")
	}
}

func TestIsExpired_CLIMode(t *testing.T) {
	// given
	now := int64(10000)
	c := newTestChecker(nil, 7200, func() int64 { return now })

	// when
	result := c.IsExpired(0)

	// then
	if result {
		t.Error("expected CLI mode (issuedAt=0) to never expire")
	}
}

func TestIsExpired_CustomExpiry(t *testing.T) {
	// given
	now := int64(10000)
	customExpiry := int64(300)
	c := newTestChecker(nil, customExpiry, func() int64 { return now })
	issuedAt := now - 301

	// when
	result := c.IsExpired(issuedAt)

	// then
	if !result {
		t.Error("expected expired with custom expiry of 300s")
	}
}

func TestNewEnvAuthChecker_EmptyEnv(t *testing.T) {
	// given
	t.Setenv("ALLOWED_SLACK_USERS", "")
	t.Setenv("BUTTON_EXPIRY_SECONDS", "")

	// when
	c := NewEnvAuthChecker()

	// then
	if c.IsAuthorized("anyone") {
		t.Error("expected deny all when env vars are empty")
	}
}

func TestParseAllowedUsers_CommaSeparated(t *testing.T) {
	// given
	raw := "U1,U2, U3"

	// when
	users := parseAllowedUsers(raw)

	// then
	expected := []string{"U1", "U2", "U3"}
	if len(users) != len(expected) {
		t.Fatalf("expected %d users, got %d", len(expected), len(users))
	}
	for i, u := range expected {
		if users[i] != u {
			t.Errorf("users[%d]=%q, want %q", i, users[i], u)
		}
	}
}
