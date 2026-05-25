package domain_test

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hironow/runops-gateway/internal/core/domain"
)

// makeJWT builds a synthetic JWT (header.payload.signature) for
// claim-parse tests. The signature segment is opaque — Phase 2d-1
// only parses the unsigned claims; signature verification belongs
// to Phase 2d-2 (per-issuer JWKs).
func makeJWT(claims map[string]any) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	body, _ := json.Marshal(claims)
	payload := base64.RawURLEncoding.EncodeToString(body)
	return header + "." + payload + ".synthetic-signature-for-test"
}

// ParseIdentityClaims extracts the JWT payload's standard claims.
// It does NOT verify the signature — that is Phase 2d-2 territory.
func TestParseIdentityClaims_HappyPath(t *testing.T) {
	jwt := makeJWT(map[string]any{
		"iss":   "https://accounts.google.com",
		"aud":   "https://broker.runops-gateway.example.com",
		"sub":   "user-12345",
		"email": "x@y.example",
		"exp":   1735689600.0, // 2025-01-01 UTC
	})
	claims, err := domain.ParseIdentityClaims(jwt)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if claims.Issuer != "https://accounts.google.com" {
		t.Errorf("Issuer = %q", claims.Issuer)
	}
	if claims.Audience != "https://broker.runops-gateway.example.com" {
		t.Errorf("Audience = %q", claims.Audience)
	}
	if claims.Subject != "user-12345" {
		t.Errorf("Subject = %q", claims.Subject)
	}
	if claims.Email != "x@y.example" {
		t.Errorf("Email = %q", claims.Email)
	}
	if !claims.ExpiresAt.Equal(time.Unix(1735689600, 0).UTC()) {
		t.Errorf("ExpiresAt = %v", claims.ExpiresAt)
	}
}

// Malformed JWTs (wrong segment count, non-base64, non-JSON payload)
// must return ErrIdentityTokenMalformed rather than panicking — the
// broker hands every inbound auth header to this parser.
func TestParseIdentityClaims_RejectsMalformed(t *testing.T) {
	cases := map[string]string{
		"empty":          "",
		"single segment": "header-only",
		"two segments":   "header.payload",
		"non-base64":     "h.!!!.s",
		"non-json":       "h." + base64.RawURLEncoding.EncodeToString([]byte("not json")) + ".s",
	}
	for name, jwt := range cases {
		_, err := domain.ParseIdentityClaims(jwt)
		if !errors.Is(err, domain.ErrIdentityTokenMalformed) {
			t.Errorf("[%s] want ErrIdentityTokenMalformed, got %v", name, err)
		}
	}
}

// JWT may include `aud` as either a string or an array of strings
// (RFC 7519 §4.1.3). The parser must accept both shapes; in the
// array case the first audience is taken (the broker pins exactly
// one audience anyway, so multi-audience tokens are not the
// expected production shape but we don't reject them outright).
func TestParseIdentityClaims_AudienceArrayShape(t *testing.T) {
	jwt := makeJWT(map[string]any{
		"iss": "https://accounts.google.com",
		"aud": []any{"https://broker.example.com", "https://other.example.com"},
		"sub": "u",
		"exp": float64(time.Now().Add(time.Hour).Unix()),
	})
	claims, err := domain.ParseIdentityClaims(jwt)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if claims.Audience != "https://broker.example.com" {
		t.Errorf("Audience = %q, want first array element", claims.Audience)
	}
}

// Validate enforces audience match + non-expired status. Each
// failure produces its own sentinel so the verifier adapter can
// distinguish "wrong audience" (= attack-shaped) from "expired"
// (= routine).
func TestIdentityClaims_Validate(t *testing.T) {
	now := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
	cases := map[string]struct {
		claims        domain.IdentityClaims
		expectedAud   string
		now           time.Time
		wantSentinel  error
		wantNoErrorOK bool
	}{
		"happy": {
			claims: domain.IdentityClaims{
				Audience:  "https://broker.example.com",
				ExpiresAt: now.Add(time.Hour),
			},
			expectedAud:   "https://broker.example.com",
			now:           now,
			wantNoErrorOK: true,
		},
		"audience mismatch": {
			claims: domain.IdentityClaims{
				Audience:  "https://attacker.example.com",
				ExpiresAt: now.Add(time.Hour),
			},
			expectedAud:  "https://broker.example.com",
			now:          now,
			wantSentinel: domain.ErrIdentityTokenAudienceMismatch,
		},
		"expired": {
			claims: domain.IdentityClaims{
				Audience:  "https://broker.example.com",
				ExpiresAt: now.Add(-time.Minute),
			},
			expectedAud:  "https://broker.example.com",
			now:          now,
			wantSentinel: domain.ErrIdentityTokenExpired,
		},
	}
	for name, c := range cases {
		err := c.claims.Validate(c.now, c.expectedAud)
		if c.wantNoErrorOK {
			if err != nil {
				t.Errorf("[%s] want nil, got %v", name, err)
			}
			continue
		}
		if !errors.Is(err, c.wantSentinel) {
			t.Errorf("[%s] want %v, got %v", name, c.wantSentinel, err)
		}
	}
}

// Audience pinning is a single fixed string — the broker URL.
// Confirm the validator does not fall through to substring match
// or case-insensitive match (= attack-shaped audience would slip
// past).
func TestIdentityClaims_Validate_AudienceExactMatchOnly(t *testing.T) {
	c := domain.IdentityClaims{
		Audience:  "https://broker.example.com",
		ExpiresAt: time.Now().Add(time.Hour),
	}
	cases := []string{
		"https://broker.example.com/",         // trailing slash
		"https://Broker.example.com",          // case difference
		" https://broker.example.com",         // leading whitespace
		"https://broker.example.com.attacker", // suffix attack
		"x" + "https://broker.example.com",    // prefix attack
	}
	for _, expectedAud := range cases {
		if err := c.Validate(time.Now(), expectedAud); err == nil {
			t.Errorf("audience %q must NOT match %q exactly, got nil", c.Audience, expectedAud)
		}
	}
}

// Round-trip: build a JWT, parse it, validate it. Catches encoding
// drift (e.g. base64 std vs URL).
func TestIdentityClaims_RoundTripFromJWT(t *testing.T) {
	now := time.Now()
	jwt := makeJWT(map[string]any{
		"iss": "https://accounts.google.com",
		"aud": "https://broker.example.com",
		"sub": "u-1",
		"exp": float64(now.Add(time.Hour).Unix()),
	})
	claims, err := domain.ParseIdentityClaims(jwt)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if err := claims.Validate(now, "https://broker.example.com"); err != nil {
		t.Errorf("Validate after parse: %v", err)
	}
}

// guard against JWT segment counts that are NOT 3 (unsigned tokens
// like Auth0's would be 2; multi-signed JWS would be 4+).
func TestParseIdentityClaims_RejectsWrongSegmentCount(t *testing.T) {
	four := strings.Join([]string{"a", "b", "c", "d"}, ".")
	if _, err := domain.ParseIdentityClaims(four); !errors.Is(err, domain.ErrIdentityTokenMalformed) {
		t.Errorf("4-segment JWT must be ErrIdentityTokenMalformed, got %v", err)
	}
}
