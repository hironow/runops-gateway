package domain

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// IdentityClaims is the broker-side decoded view of a JWT identity
// token (refs#0007 plan v8 §5.1 caller authentication paths). The
// 4 verifier adapters (Cloud Run IAM / Workload Identity / gcloud
// identity / delegated agent) all consume tokens that share the
// same claim shape; this type is the common ground.
//
// Phase 2d-1 (this file) only PARSES claims — signature verification
// is Phase 2d-2 territory because each issuer (Google STS / Cloud
// Run / workspace daemon) has its own JWKs endpoint and rotation
// policy. Parse + Validate let unit tests pin the audience /
// expiry rules before the per-issuer signature work begins.
type IdentityClaims struct {
	Issuer    string
	Audience  string
	Subject   string
	Email     string
	ExpiresAt time.Time
}

// rawClaims mirrors the JWT payload's standard fields; aud may be
// either string or []string per RFC 7519 §4.1.3.
type rawClaims struct {
	Iss   string `json:"iss"`
	Aud   any    `json:"aud"`
	Sub   string `json:"sub"`
	Email string `json:"email"`
	Exp   int64  `json:"exp"`
}

// ParseIdentityClaims base64url-decodes the JWT payload and extracts
// the standard claims. Signature verification is NOT performed here
// — every caller MUST follow up with a per-issuer JWKs verify
// before trusting the claims. The function returns
// ErrIdentityTokenMalformed for any structural failure (wrong
// segment count, non-base64, non-JSON, missing required fields).
func ParseIdentityClaims(jwt string) (IdentityClaims, error) {
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		return IdentityClaims{}, ErrIdentityTokenMalformed
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return IdentityClaims{}, ErrIdentityTokenMalformed
	}
	var raw rawClaims
	if err := json.Unmarshal(body, &raw); err != nil {
		return IdentityClaims{}, ErrIdentityTokenMalformed
	}
	aud, err := normaliseAudience(raw.Aud)
	if err != nil {
		return IdentityClaims{}, err
	}
	return IdentityClaims{
		Issuer:    raw.Iss,
		Audience:  aud,
		Subject:   raw.Sub,
		Email:     raw.Email,
		ExpiresAt: time.Unix(raw.Exp, 0).UTC(),
	}, nil
}

func normaliseAudience(raw any) (string, error) {
	switch v := raw.(type) {
	case string:
		return v, nil
	case []any:
		if len(v) == 0 {
			return "", ErrIdentityTokenMalformed
		}
		first, ok := v[0].(string)
		if !ok {
			return "", ErrIdentityTokenMalformed
		}
		return first, nil
	case nil:
		return "", nil
	default:
		return "", ErrIdentityTokenMalformed
	}
}

// Validate confirms the token's audience exactly matches the broker's
// pinned audience and the token has not expired. Audience match is
// EXACT — no trailing slash, no case folding, no substring tolerance
// — so attack-shaped audiences (`broker.example.com.attacker`,
// `Broker.example.com`, etc.) cannot slip past.
func (c IdentityClaims) Validate(now time.Time, expectedAudience string) error {
	if c.Audience != expectedAudience {
		return ErrIdentityTokenAudienceMismatch
	}
	if !now.Before(c.ExpiresAt) {
		return ErrIdentityTokenExpired
	}
	return nil
}

// Sentinel errors raised by IdentityClaims parse / validate. Each
// failure mode is its own sentinel so the verifier adapter can
// audit attack-shaped attempts (audience mismatch / malformed)
// distinctly from routine lifecycle events (expired).
var (
	ErrIdentityTokenMalformed        = errors.New("identity_token: malformed")
	ErrIdentityTokenAudienceMismatch = errors.New("identity_token: audience does not match broker audience")
	ErrIdentityTokenExpired          = errors.New("identity_token: expired")
)
