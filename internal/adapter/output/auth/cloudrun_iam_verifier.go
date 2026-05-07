package auth

import (
	"errors"

	"github.com/hironow/runops-gateway/internal/core/domain"
)

// CloudRunIAMVerifier authenticates the gateway-service caller path
// (refs#0007 plan v8 §5.1). The expected bearer is a Cloud Run
// service-to-service IAM identity token: another Cloud Run service
// in the project requests a Google STS identity token whose
// `email` claim is its own service account, and presents it to
// the broker.
//
// Composition (same as GcloudIdentityTokenVerifier — Phase 2d-2c):
//
//  1. JWKSVerifier (Phase 2d-2b) — RS256 signature, kid lookup,
//     audience exact-match, expiry.
//  2. Issuer pin — only Google STS-issued tokens are accepted.
//  3. Service account allowlist — the caller's `email` claim
//     MUST be on the configured Cloud Run service-account
//     allowlist. Unlike the human-operator verifier, EMPTY
//     allowlist is REJECTED at construction time: the
//     gateway-service caller path is internal infrastructure
//     and any-Cloud-Run-service trust is too broad.
//
// On success: BrokerActor{Type: CallerGatewayService, UserEmail: <SA email>}.
type CloudRunIAMVerifier struct {
	signature   jwtSignatureVerifier
	issuer      string
	saAllowlist map[string]struct{}
}

// NewCloudRunIAMVerifier wires production with the configured JWKS
// verifier, issuer URL, and service-account allowlist. Panics with
// ErrCloudRunIAMRequiresAllowlist if the allowlist is empty —
// see the Phase 2d-2d ADR rationale (defence in depth at ctor
// time, not runtime).
func NewCloudRunIAMVerifier(jwks *JWKSVerifier, issuer string, allowedSAs []string) (*CloudRunIAMVerifier, error) {
	return newCloudRunIAMVerifierFromConfig(jwks, issuer, allowedSAs)
}

func newCloudRunIAMVerifierFromConfig(sig jwtSignatureVerifier, issuer string, emails []string) (*CloudRunIAMVerifier, error) {
	if len(emails) == 0 {
		return nil, ErrCloudRunIAMRequiresAllowlist
	}
	allow := make(map[string]struct{}, len(emails))
	for _, e := range emails {
		allow[e] = struct{}{}
	}
	return &CloudRunIAMVerifier{
		signature:   sig,
		issuer:      issuer,
		saAllowlist: allow,
	}, nil
}

// newCloudRunIAMVerifierWithVerifier is the test-only ctor: it
// shares the same internal seam as the gcloud verifier so the same
// fakeSignatureVerifier double can drive both adapters.
func newCloudRunIAMVerifierWithVerifier(sig jwtSignatureVerifier, issuer string, emails []string) *CloudRunIAMVerifier {
	v, err := newCloudRunIAMVerifierFromConfig(sig, issuer, emails)
	if err != nil {
		// Tests that exercise the empty-allowlist rejection use the
		// from-config ctor directly; the with-verifier path always
		// supplies a non-empty allowlist, so a panic here means a
		// test bug (not a production reachable path).
		panic(err)
	}
	return v
}

// VerifyBearerToken runs the pipeline and returns a verified
// CallerGatewayService BrokerActor on success.
func (v *CloudRunIAMVerifier) VerifyBearerToken(jwtToken string) (domain.BrokerActor, error) {
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
		Type:      domain.CallerGatewayService,
		UserEmail: claims.Email,
	}, nil
}

// ErrCloudRunIAMRequiresAllowlist is the ctor-time guard that
// prevents a misconfigured deployment from accepting any Cloud
// Run service in the project as gateway-service.
var ErrCloudRunIAMRequiresAllowlist = errors.New("cloudrun_iam: service-account allowlist must be non-empty")
