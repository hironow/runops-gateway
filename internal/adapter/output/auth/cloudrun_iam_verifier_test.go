package auth

import (
	"errors"
	"testing"

	"github.com/hironow/runops-gateway/internal/core/domain"
)

const cloudRunIssuer = "https://accounts.google.com"

// Happy path: valid Google STS-signed identity token from a
// Cloud Run service whose SA email is on the allowlist.
func TestCloudRunIAMVerifier_VerifyBearerToken_HappyPath(t *testing.T) {
	v := newCloudRunIAMVerifierWithVerifier(
		&fakeSignatureVerifier{claims: domain.IdentityClaims{
			Issuer: cloudRunIssuer,
			Email:  "gateway-internal@gen-ai-hironow.iam.gserviceaccount.com",
		}},
		cloudRunIssuer,
		[]string{"gateway-internal@gen-ai-hironow.iam.gserviceaccount.com"},
	)
	actor, err := v.VerifyBearerToken("any-jwt")
	if err != nil {
		t.Fatalf("happy: %v", err)
	}
	if actor.Type != domain.CallerGatewayService {
		t.Errorf("Type = %q, want gateway-service", actor.Type)
	}
	if actor.UserEmail != "gateway-internal@gen-ai-hironow.iam.gserviceaccount.com" {
		t.Errorf("UserEmail = %q", actor.UserEmail)
	}
}

// SA not in the allowlist: a different Cloud Run service signing
// with a valid Google STS token must NOT be accepted as gateway-service.
func TestCloudRunIAMVerifier_VerifyBearerToken_SANotInAllowlistRejected(t *testing.T) {
	v := newCloudRunIAMVerifierWithVerifier(
		&fakeSignatureVerifier{claims: domain.IdentityClaims{
			Issuer: cloudRunIssuer,
			Email:  "another-cloudrun-service@gen-ai-hironow.iam.gserviceaccount.com",
		}},
		cloudRunIssuer,
		[]string{"gateway-internal@gen-ai-hironow.iam.gserviceaccount.com"},
	)
	_, err := v.VerifyBearerToken("any-jwt")
	if !errors.Is(err, ErrEmailNotInAllowlist) {
		t.Errorf("want ErrEmailNotInAllowlist, got %v", err)
	}
}

// Cloud Run service-to-service IAM tokens are NOT meant to be
// callable with empty allowlist — that would let any Cloud Run
// service in the project mint broker tokens. The verifier must
// REJECT empty allowlist construction (this is a CTOR-time
// invariant, not a runtime check).
func TestCloudRunIAMVerifier_NewRejectsEmptyAllowlist(t *testing.T) {
	for _, emails := range [][]string{nil, {}} {
		_, err := newCloudRunIAMVerifierFromConfig(&fakeSignatureVerifier{}, cloudRunIssuer, emails)
		if !errors.Is(err, ErrCloudRunIAMRequiresAllowlist) {
			t.Errorf("emails=%v: want ErrCloudRunIAMRequiresAllowlist, got %v", emails, err)
		}
	}
}

// Issuer mismatch (token signed by something other than Google STS)
// is rejected even if the SA appears in the allowlist.
func TestCloudRunIAMVerifier_VerifyBearerToken_IssuerMismatchRejected(t *testing.T) {
	v := newCloudRunIAMVerifierWithVerifier(
		&fakeSignatureVerifier{claims: domain.IdentityClaims{
			Issuer: "https://attacker-idp.example.com",
			Email:  "gateway-internal@gen-ai-hironow.iam.gserviceaccount.com",
		}},
		cloudRunIssuer,
		[]string{"gateway-internal@gen-ai-hironow.iam.gserviceaccount.com"},
	)
	_, err := v.VerifyBearerToken("any-jwt")
	if !errors.Is(err, ErrIssuerMismatch) {
		t.Errorf("want ErrIssuerMismatch, got %v", err)
	}
}

// Signature-layer failures propagate via errors.Is so the broker
// handler can distinguish them in the audit log.
func TestCloudRunIAMVerifier_VerifyBearerToken_SignatureFailurePropagates(t *testing.T) {
	wantErr := domain.ErrIdentityTokenAudienceMismatch
	v := newCloudRunIAMVerifierWithVerifier(
		&fakeSignatureVerifier{err: wantErr},
		cloudRunIssuer,
		[]string{"sa@example.com"},
	)
	_, err := v.VerifyBearerToken("any-jwt")
	if !errors.Is(err, wantErr) {
		t.Errorf("want %v, got %v", wantErr, err)
	}
}
