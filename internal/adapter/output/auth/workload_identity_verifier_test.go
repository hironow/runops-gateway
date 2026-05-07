package auth

import (
	"errors"
	"testing"

	"github.com/hironow/runops-gateway/internal/core/domain"
)

const workloadIssuer = "https://accounts.google.com"

// Happy path: workspace daemon GCE VM presents a Google STS
// identity token whose `email` claim is the workspace daemon SA.
func TestWorkloadIdentityVerifier_VerifyBearerToken_HappyPath(t *testing.T) {
	v := newWorkloadIdentityVerifierWithVerifier(
		&fakeSignatureVerifier{claims: domain.IdentityClaims{
			Issuer: workloadIssuer,
			Email:  "workspace-daemon@gen-ai-hironow.iam.gserviceaccount.com",
		}},
		workloadIssuer,
		[]string{"workspace-daemon@gen-ai-hironow.iam.gserviceaccount.com"},
	)
	actor, err := v.VerifyBearerToken("any-jwt")
	if err != nil {
		t.Fatalf("happy: %v", err)
	}
	if actor.Type != domain.CallerWorkspaceDaemon {
		t.Errorf("Type = %q, want workspace-daemon", actor.Type)
	}
	if actor.UserEmail != "workspace-daemon@gen-ai-hironow.iam.gserviceaccount.com" {
		t.Errorf("UserEmail = %q", actor.UserEmail)
	}
}

// SA not in the configured allowlist is rejected — a different
// GCE workload's identity token must NOT be accepted as
// workspace-daemon even with a valid Google STS signature.
func TestWorkloadIdentityVerifier_VerifyBearerToken_SANotInAllowlistRejected(t *testing.T) {
	v := newWorkloadIdentityVerifierWithVerifier(
		&fakeSignatureVerifier{claims: domain.IdentityClaims{
			Issuer: workloadIssuer,
			Email:  "another-gce-workload@gen-ai-hironow.iam.gserviceaccount.com",
		}},
		workloadIssuer,
		[]string{"workspace-daemon@gen-ai-hironow.iam.gserviceaccount.com"},
	)
	_, err := v.VerifyBearerToken("any-jwt")
	if !errors.Is(err, ErrEmailNotInAllowlist) {
		t.Errorf("want ErrEmailNotInAllowlist, got %v", err)
	}
}

// Empty allowlist is REJECTED at construction time — same
// rationale as Phase 2d-2d (cloudrun_iam): workspace-daemon is
// internal infrastructure, "any GCE workload in the project" is
// too broad a trust boundary.
func TestWorkloadIdentityVerifier_NewRejectsEmptyAllowlist(t *testing.T) {
	for _, emails := range [][]string{nil, {}} {
		_, err := newWorkloadIdentityVerifierFromConfig(&fakeSignatureVerifier{}, workloadIssuer, emails)
		if !errors.Is(err, ErrWorkloadIdentityRequiresAllowlist) {
			t.Errorf("emails=%v: want ErrWorkloadIdentityRequiresAllowlist, got %v", emails, err)
		}
	}
}

// Issuer mismatch is rejected even with a matching SA email.
func TestWorkloadIdentityVerifier_VerifyBearerToken_IssuerMismatchRejected(t *testing.T) {
	v := newWorkloadIdentityVerifierWithVerifier(
		&fakeSignatureVerifier{claims: domain.IdentityClaims{
			Issuer: "https://attacker-idp.example.com",
			Email:  "workspace-daemon@gen-ai-hironow.iam.gserviceaccount.com",
		}},
		workloadIssuer,
		[]string{"workspace-daemon@gen-ai-hironow.iam.gserviceaccount.com"},
	)
	_, err := v.VerifyBearerToken("any-jwt")
	if !errors.Is(err, ErrIssuerMismatch) {
		t.Errorf("want ErrIssuerMismatch, got %v", err)
	}
}

// Signature-layer failures propagate.
func TestWorkloadIdentityVerifier_VerifyBearerToken_SignatureFailurePropagates(t *testing.T) {
	wantErr := domain.ErrIdentityTokenAudienceMismatch
	v := newWorkloadIdentityVerifierWithVerifier(
		&fakeSignatureVerifier{err: wantErr},
		workloadIssuer,
		[]string{"sa@example.com"},
	)
	_, err := v.VerifyBearerToken("any-jwt")
	if !errors.Is(err, wantErr) {
		t.Errorf("want %v, got %v", wantErr, err)
	}
}
