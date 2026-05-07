package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// InstallationToken is the broker's response payload (plan v8 §5.5).
// The Token field travels EXACTLY ONCE — through the HTTP response
// body to the caller — and is then forgotten by the broker. It must
// never appear in logs, OTel attributes, D-Mail, Pub/Sub, or
// archives; the gateway-broker-token-leak Semgrep rules under
// .semgrep/rules/release-gate/ enforce this at code-review time.
type InstallationToken struct {
	Token            string                `json:"token"`
	ExpiresAt        time.Time             `json:"expires_at"`
	Actor            BrokerActor           `json:"actor"`
	ProjectID        string                `json:"project_id"`
	Tool             Tool                  `json:"tool"`
	Permissions      RepositoryPermissions `json:"permissions"`
	AuditFingerprint string                `json:"audit_fingerprint"`
}

// BrokerActor mirrors the response `actor` object from plan v8 §5.5.
// Every field is broker-derived from the verified caller credential
// — callers may NOT self-claim any of these (plan v8 §5.4 schema
// lockdown).
type BrokerActor struct {
	Type      CallerType `json:"type"`
	UserEmail string     `json:"user_email,omitempty"`
	SessionID string     `json:"session_id,omitempty"`
}

// AuditFingerprint returns the lowercase-hex first-8-byte prefix of
// sha256(token). This is the ONLY token-derived value plan v8 §5.5
// permits in audit logs, OTel attributes, or any other persistent
// surface. Callers must never log the raw token alongside the
// fingerprint either — that would defeat the entire purpose.
func AuditFingerprint(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:8])
}
