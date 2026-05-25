package auth

import (
	"errors"
	"testing"
	"time"

	"github.com/hironow/runops-gateway/internal/core/domain"
)

// fakeSignatureVerifier stands in for JWKSVerifier so the gcloud
// verifier's orchestration (issuer pin + allowlist) can be unit
// tested without spinning up an httptest JWKS server.
type fakeSignatureVerifier struct {
	claims domain.IdentityClaims
	err    error
}

func (f *fakeSignatureVerifier) VerifyAndParse(_ string) (domain.IdentityClaims, error) {
	return f.claims, f.err
}

const googleIssuer = "https://accounts.google.com"

// Happy path: signature ok, issuer = google STS, email in allowlist.
func TestGcloudIdentityTokenVerifier_VerifyBearerToken_HappyPath(t *testing.T) {
	v := newWithVerifier(
		&fakeSignatureVerifier{claims: domain.IdentityClaims{
			Issuer: googleIssuer,
			Email:  "operator@example.com",
		}},
		googleIssuer,
		[]string{"operator@example.com"},
	)
	actor, err := v.VerifyBearerToken("any-jwt")
	if err != nil {
		t.Fatalf("happy: %v", err)
	}
	if actor.Type != domain.CallerHumanOperator {
		t.Errorf("Type = %q, want human-operator", actor.Type)
	}
	if actor.UserEmail != "operator@example.com" {
		t.Errorf("UserEmail = %q", actor.UserEmail)
	}
}

// Empty allowlist (= no allowlist enforcement) accepts any verified
// email. This is the bootstrap config — production deployments
// should always provide a non-empty allowlist.
func TestGcloudIdentityTokenVerifier_VerifyBearerToken_EmptyAllowlistAcceptsAny(t *testing.T) {
	v := newWithVerifier(
		&fakeSignatureVerifier{claims: domain.IdentityClaims{
			Issuer: googleIssuer,
			Email:  "anyone@example.com",
		}},
		googleIssuer,
		nil,
	)
	if _, err := v.VerifyBearerToken("any-jwt"); err != nil {
		t.Fatalf("empty allowlist must accept any verified caller: %v", err)
	}
}

// Email not in the configured allowlist is rejected. This is the
// authorisation layer on top of authentication — a verified
// Google identity that is not on the operator allowlist must NOT
// receive a broker token.
func TestGcloudIdentityTokenVerifier_VerifyBearerToken_EmailNotInAllowlistRejected(t *testing.T) {
	v := newWithVerifier(
		&fakeSignatureVerifier{claims: domain.IdentityClaims{
			Issuer: googleIssuer,
			Email:  "stranger@example.com",
		}},
		googleIssuer,
		[]string{"operator@example.com"},
	)
	_, err := v.VerifyBearerToken("any-jwt")
	if !errors.Is(err, ErrEmailNotInAllowlist) {
		t.Errorf("want ErrEmailNotInAllowlist, got %v", err)
	}
}

// Issuer mismatch (signature OK but the issuer is not the pinned
// Google STS) is rejected. Without this check, a forged JWKS-backed
// IDP could mint operator-scoped tokens.
func TestGcloudIdentityTokenVerifier_VerifyBearerToken_IssuerMismatchRejected(t *testing.T) {
	v := newWithVerifier(
		&fakeSignatureVerifier{claims: domain.IdentityClaims{
			Issuer: "https://attacker-idp.example.com",
			Email:  "operator@example.com",
		}},
		googleIssuer,
		[]string{"operator@example.com"},
	)
	_, err := v.VerifyBearerToken("any-jwt")
	if !errors.Is(err, ErrIssuerMismatch) {
		t.Errorf("want ErrIssuerMismatch, got %v", err)
	}
}

// Signature-layer failures (audience mismatch, expired, malformed,
// bad signature) propagate unchanged so the broker handler can
// render them with the right HTTP status.
func TestGcloudIdentityTokenVerifier_VerifyBearerToken_SignatureFailurePropagates(t *testing.T) {
	wantErr := domain.ErrIdentityTokenExpired
	v := newWithVerifier(
		&fakeSignatureVerifier{err: wantErr},
		googleIssuer,
		[]string{"operator@example.com"},
	)
	_, err := v.VerifyBearerToken("any-jwt")
	if !errors.Is(err, wantErr) {
		t.Errorf("want %v, got %v", wantErr, err)
	}
}

// Audience mismatch from the signature layer also propagates as-is.
func TestGcloudIdentityTokenVerifier_VerifyBearerToken_AudienceMismatchPropagates(t *testing.T) {
	wantErr := domain.ErrIdentityTokenAudienceMismatch
	v := newWithVerifier(
		&fakeSignatureVerifier{err: wantErr},
		googleIssuer,
		nil,
	)
	_, err := v.VerifyBearerToken("any-jwt")
	if !errors.Is(err, wantErr) {
		t.Errorf("want %v, got %v", wantErr, err)
	}
}

// time.Now() is unused inside this verifier (every time-dependent
// check happens in the wrapped JWKSVerifier), so we don't bother
// injecting it. Keep this assertion explicit so a future refactor
// that adds time-of-check logic also adds the injectable seam.
func TestGcloudIdentityTokenVerifier_NoTimeDependency(t *testing.T) {
	// Build the verifier with a fake that always succeeds. If the
	// orchestration were to add time-of-check logic without a
	// seam, the test below would need a `now` field; lack of one
	// is the assertion.
	v := newWithVerifier(
		&fakeSignatureVerifier{claims: domain.IdentityClaims{
			Issuer:    googleIssuer,
			Email:     "operator@example.com",
			ExpiresAt: time.Time{}, // zero value — irrelevant
		}},
		googleIssuer,
		[]string{"operator@example.com"},
	)
	if _, err := v.VerifyBearerToken("any-jwt"); err != nil {
		t.Errorf("zero-value ExpiresAt must not affect outcome here: %v", err)
	}
}
