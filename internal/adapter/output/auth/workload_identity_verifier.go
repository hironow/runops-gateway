package auth

import (
	"errors"

	"github.com/hironow/runops-gateway/internal/core/domain"
)

// WorkloadIdentityVerifier authenticates the workspace-daemon
// caller path (refs#0007 plan v8 §5.1). The expected bearer is a
// Google STS identity token requested by a workspace-daemon
// process from the GCE metadata service; the `email` claim is
// the workspace daemon's service account.
//
// Composition (same shape as the cloudrun_iam verifier in
// Phase 2d-2d):
//
//  1. JWKSVerifier — RS256 signature, kid lookup, audience
//     exact-match, expiry.
//  2. Issuer pin — only Google STS-issued tokens are accepted.
//  3. Service-account allowlist — the caller's `email` claim
//     MUST match the configured workspace-daemon SA. EMPTY
//     allowlist is REJECTED at construction time:
//     workspace-daemon is internal infrastructure, "any GCE
//     workload in the project" is too broad a trust boundary.
//
// On success: BrokerActor{Type: CallerWorkspaceDaemon, UserEmail: <SA email>}.
type WorkloadIdentityVerifier struct {
	signature   jwtSignatureVerifier
	issuer      string
	saAllowlist map[string]struct{}
}

// NewWorkloadIdentityVerifier wires production. Returns
// ErrWorkloadIdentityRequiresAllowlist if the allowlist is empty —
// the deployment must always configure the workspace-daemon SA(s)
// it expects.
func NewWorkloadIdentityVerifier(jwks *JWKSVerifier, issuer string, allowedSAs []string) (*WorkloadIdentityVerifier, error) {
	return newWorkloadIdentityVerifierFromConfig(jwks, issuer, allowedSAs)
}

func newWorkloadIdentityVerifierFromConfig(sig jwtSignatureVerifier, issuer string, emails []string) (*WorkloadIdentityVerifier, error) {
	if len(emails) == 0 {
		return nil, ErrWorkloadIdentityRequiresAllowlist
	}
	allow := make(map[string]struct{}, len(emails))
	for _, e := range emails {
		allow[e] = struct{}{}
	}
	return &WorkloadIdentityVerifier{
		signature:   sig,
		issuer:      issuer,
		saAllowlist: allow,
	}, nil
}

// newWorkloadIdentityVerifierWithVerifier is the test-only ctor
// that shares the unexported jwtSignatureVerifier seam with the
// other verifiers in this package.
func newWorkloadIdentityVerifierWithVerifier(sig jwtSignatureVerifier, issuer string, emails []string) *WorkloadIdentityVerifier {
	v, err := newWorkloadIdentityVerifierFromConfig(sig, issuer, emails)
	if err != nil {
		panic(err)
	}
	return v
}

// VerifyBearerToken runs the pipeline and returns a verified
// CallerWorkspaceDaemon BrokerActor on success.
func (v *WorkloadIdentityVerifier) VerifyBearerToken(jwtToken string) (domain.BrokerActor, error) {
	claims, err := v.signature.VerifyAndParse(jwtToken)
	if err != nil {
		return domain.BrokerActor{}, err
	}
	if claims.Issuer != v.issuer {
		return domain.BrokerActor{}, ErrIssuerMismatch
	}
	if _, ok := v.saAllowlist[claims.Email]; !ok {
		return domain.BrokerActor{}, ErrEmailNotInAllowlist
	}
	return domain.BrokerActor{
		Type:      domain.CallerWorkspaceDaemon,
		UserEmail: claims.Email,
	}, nil
}

// ErrWorkloadIdentityRequiresAllowlist is the ctor-time guard
// preventing a deployment from accepting any GCE workload in the
// project as workspace-daemon.
var ErrWorkloadIdentityRequiresAllowlist = errors.New("workload_identity: SA allowlist must be non-empty")
