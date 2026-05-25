package auth_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/hironow/runops-gateway/internal/adapter/output/auth"
	"github.com/hironow/runops-gateway/internal/core/domain"
)

// jwksTestSetup boots an httptest.Server that serves a single-key
// JWKS document and returns helpers for signing test JWTs against
// that key (so `verifier.VerifyAndParse` exercises the real
// signature path end-to-end without network calls to Google STS).
type jwksTestSetup struct {
	server  *httptest.Server
	jwksURL string
	priv    *rsa.PrivateKey
	kid     string
}

func newJWKSTestSetup(t *testing.T) *jwksTestSetup {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	kid := "test-kid-001"
	mux := http.NewServeMux()
	mux.HandleFunc("/jwks.json", func(w http.ResponseWriter, _ *http.Request) {
		nB := priv.N.Bytes()
		eB := big.NewInt(int64(priv.E)).Bytes()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]string{
				{
					"kid": kid,
					"kty": "RSA",
					"alg": "RS256",
					"use": "sig",
					"n":   base64.RawURLEncoding.EncodeToString(nB),
					"e":   base64.RawURLEncoding.EncodeToString(eB),
				},
			},
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &jwksTestSetup{
		server:  srv,
		jwksURL: srv.URL + "/jwks.json",
		priv:    priv,
		kid:     kid,
	}
}

// signJWT signs claims with the test setup's RSA key, optionally
// using a different kid (= unknown-kid test) or different alg.
func (s *jwksTestSetup) signJWT(t *testing.T, claims jwt.MapClaims, opts ...func(*jwt.Token)) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = s.kid
	for _, o := range opts {
		o(tok)
	}
	signed, err := tok.SignedString(s.priv)
	if err != nil {
		t.Fatalf("SignedString: %v", err)
	}
	return signed
}

// signWithDifferentKey returns a JWT signed by a *different* RSA key
// (= signature mismatch when verified against the JWKS server's
// advertised key).
func (s *jwksTestSetup) signWithDifferentKey(t *testing.T, claims jwt.MapClaims) string {
	t.Helper()
	other, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = s.kid
	signed, err := tok.SignedString(other)
	if err != nil {
		t.Fatalf("SignedString: %v", err)
	}
	return signed
}

const audience = "https://broker.example.com"

func freshJWKSClaims(now time.Time) jwt.MapClaims {
	return jwt.MapClaims{
		"iss":   "https://accounts.google.com",
		"aud":   audience,
		"sub":   "user-12345",
		"email": "x@y.example",
		"exp":   float64(now.Add(time.Hour).Unix()),
	}
}

// Happy path: valid signature + matching kid + audience + non-expired.
func TestJWKSVerifier_VerifyAndParse_HappyPath(t *testing.T) {
	now := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
	setup := newJWKSTestSetup(t)
	v, err := auth.NewJWKSVerifier(context.Background(), setup.jwksURL, audience, func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewJWKSVerifier: %v", err)
	}
	signed := setup.signJWT(t, freshJWKSClaims(now))
	claims, err := v.VerifyAndParse(signed)
	if err != nil {
		t.Fatalf("VerifyAndParse: %v", err)
	}
	if claims.Audience != audience {
		t.Errorf("Audience = %q", claims.Audience)
	}
	if claims.Email != "x@y.example" {
		t.Errorf("Email = %q", claims.Email)
	}
}

// Bad signature (signed with a different RSA key) MUST be rejected
// — this is the core attack surface JWKs verification exists to
// close. Without keyfunc, an attacker who only sees the public JWKS
// could forge tokens.
func TestJWKSVerifier_VerifyAndParse_BadSignatureRejected(t *testing.T) {
	now := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
	setup := newJWKSTestSetup(t)
	v, _ := auth.NewJWKSVerifier(context.Background(), setup.jwksURL, audience, func() time.Time { return now })
	tampered := setup.signWithDifferentKey(t, freshJWKSClaims(now))
	_, err := v.VerifyAndParse(tampered)
	if err == nil {
		t.Fatalf("forged signature must be rejected, got nil")
	}
}

// Unknown kid (= signed claim header references a key the JWKS
// server does not advertise) must be rejected.
func TestJWKSVerifier_VerifyAndParse_UnknownKidRejected(t *testing.T) {
	now := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
	setup := newJWKSTestSetup(t)
	v, _ := auth.NewJWKSVerifier(context.Background(), setup.jwksURL, audience, func() time.Time { return now })
	signed := setup.signJWT(t, freshJWKSClaims(now), func(tok *jwt.Token) {
		tok.Header["kid"] = "kid-that-does-not-exist"
	})
	_, err := v.VerifyAndParse(signed)
	if err == nil {
		t.Fatalf("unknown kid must be rejected, got nil")
	}
}

// alg=none attack: a JWT with header alg=none MUST be rejected.
// jwt.WithValidMethods([]string{"RS256"}) should enforce this; we
// add an explicit test so a future refactor that drops the option
// is caught immediately.
func TestJWKSVerifier_VerifyAndParse_AlgNoneAttackRejected(t *testing.T) {
	now := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
	setup := newJWKSTestSetup(t)
	v, _ := auth.NewJWKSVerifier(context.Background(), setup.jwksURL, audience, func() time.Time { return now })

	// Manually craft an unsigned JWT with alg=none.
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","kid":"` + setup.kid + `"}`))
	payload, _ := json.Marshal(freshJWKSClaims(now))
	body := base64.RawURLEncoding.EncodeToString(payload)
	noneToken := header + "." + body + "."
	if _, err := v.VerifyAndParse(noneToken); err == nil {
		t.Fatalf("alg=none must be rejected, got nil")
	}
}

// Audience mismatch (sig OK, but aud wrong) → ErrIdentityTokenAudienceMismatch.
// The verifier composes signature verification (keyfunc) with
// claim validation (domain.IdentityClaims.Validate); audience
// drift between issuer and broker MUST surface here.
func TestJWKSVerifier_VerifyAndParse_AudienceMismatchSurfaces(t *testing.T) {
	now := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
	setup := newJWKSTestSetup(t)
	v, _ := auth.NewJWKSVerifier(context.Background(), setup.jwksURL, audience, func() time.Time { return now })
	c := freshJWKSClaims(now)
	c["aud"] = "https://attacker.example.com"
	signed := setup.signJWT(t, c)
	_, err := v.VerifyAndParse(signed)
	if !errors.Is(err, domain.ErrIdentityTokenAudienceMismatch) {
		t.Errorf("audience mismatch want ErrIdentityTokenAudienceMismatch, got %v", err)
	}
}

// Expired token (signature OK, but exp in the past) →
// ErrIdentityTokenExpired.
func TestJWKSVerifier_VerifyAndParse_ExpiredSurfaces(t *testing.T) {
	now := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
	setup := newJWKSTestSetup(t)
	v, _ := auth.NewJWKSVerifier(context.Background(), setup.jwksURL, audience, func() time.Time { return now })
	c := freshJWKSClaims(now)
	c["exp"] = float64(now.Add(-time.Minute).Unix())
	signed := setup.signJWT(t, c)
	_, err := v.VerifyAndParse(signed)
	// jwt.Parse treats exp as required and may fail at the parse
	// stage with its own ErrTokenExpired, OR our domain.Validate
	// surfaces the sentinel. Either is acceptable as long as the
	// token is REJECTED. We pin "rejected" rather than the exact
	// sentinel because golang-jwt's pre-validation runs before our
	// domain check — surface either side.
	if err == nil {
		t.Errorf("expired token must be rejected, got nil")
	}
	_ = fmt.Sprintf("%v", err) // touch err to satisfy the linter
}
