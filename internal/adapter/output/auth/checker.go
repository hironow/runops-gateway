package auth

import (
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
)

const defaultExpirySeconds = int64(7200)

// EnvAuthChecker implements port.AuthChecker using environment variables.
type EnvAuthChecker struct {
	allowedUsers  []string
	expirySeconds int64
	now           func() int64 // injectable for testing
}

// NewEnvAuthChecker reads allowed users from ALLOWED_SLACK_USERS env var.
// If BUTTON_EXPIRY_SECONDS is set, it overrides the default 7200s expiry.
func NewEnvAuthChecker() *EnvAuthChecker {
	allowed := parseAllowedUsers(os.Getenv("ALLOWED_SLACK_USERS"))
	expiry := defaultExpirySeconds
	if v := os.Getenv("BUTTON_EXPIRY_SECONDS"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			expiry = n
		}
	}
	return &EnvAuthChecker{
		allowedUsers:  allowed,
		expirySeconds: expiry,
		now:           func() int64 { return time.Now().Unix() },
	}
}

// IsAuthorized reports whether approverID is in the allowed users list.
// Returns false if the allowed list is empty (deny by default).
func (c *EnvAuthChecker) IsAuthorized(approverID string) bool {
	return slices.Contains(c.allowedUsers, approverID)
}

// IsExpired reports whether the issuedAt timestamp has exceeded the expiry window.
// issuedAt == 0 means CLI mode — never expired.
func (c *EnvAuthChecker) IsExpired(issuedAt int64) bool {
	if issuedAt == 0 {
		return false // CLI mode: no expiry
	}
	return c.now()-issuedAt > c.expirySeconds
}

func parseAllowedUsers(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
