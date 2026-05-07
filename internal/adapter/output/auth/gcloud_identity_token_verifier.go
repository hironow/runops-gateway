package auth

import (
	"errors"

	"github.com/hironow/runops-gateway/internal/core/domain"
)

// GcloudIdentityTokenVerifier authenticates human-operator callers
// (refs#0007 plan v8 §5.1). The expected bearer is the output of
// `gcloud auth print-identity-token --audiences=<broker>`, signed
// by Google STS (issuer = "https://accounts.google.com").
//
// The verifier composes:
//
//  1. JWKSVerifier (Phase 2d-2b) — RS256 signature, kid lookup,
//     audience exact-match, expiry. Cryptography is delegated to
//     the well-tested `keyfunc/v3` + `golang-jwt/v5` stack.
//  2. Issuer pin — only Google STS-issued tokens are accepted. A
//     forged JWKS-backed IDP could otherwise mint operator-scoped
//     tokens.
//  3. Email allowlist — authenticated Google identities not on
//     the operator allowlist are rejected. Empty allowlist (= no
//     enforcement) is the bootstrap config; production must always
//     configure a non-empty allowlist via env var (handled by the
//     composition root in Phase 3b).
type GcloudIdentityTokenVerifier struct {
	signature      jwtSignatureVerifier
	issuer         string
	emailAllowlist map[string]struct{}
}

// jwtSignatureVerifier is the seam between this verifier's
// orchestration and the JWKs/signature path. Production wiring uses
// *JWKSVerifier; tests substitute a fake.
type jwtSignatureVerifier interface {
	VerifyAndParse(jwtToken string) (domain.IdentityClaims, error)
}

// NewGcloudIdentityTokenVerifier wires a production verifier with
// the configured JWKS verifier, issuer URL, and operator email
// allowlist. An empty `emails` slice or nil disables allowlist
// enforcement.
func NewGcloudIdentityTokenVerifier(jwks *JWKSVerifier, issuer string, emails []string) *GcloudIdentityTokenVerifier {
	return newWithVerifier(jwks, issuer, emails)
}

// newWithVerifier is the package-internal ctor used by tests. It is
// unexported so production callers must use NewGcloudIdentityTokenVerifier
// (= they get the real JWKSVerifier signature path, not a fake).
func newWithVerifier(sig jwtSignatureVerifier, issuer string, emails []string) *GcloudIdentityTokenVerifier {
	var allow map[string]struct{}
	if len(emails) > 0 {
		allow = make(map[string]struct{}, len(emails))
		for _, e := range emails {
			allow[e] = struct{}{}
		}
	}
	return &GcloudIdentityTokenVerifier{
		signature:      sig,
		issuer:         issuer,
		emailAllowlist: allow,
	}
}

// VerifyBearerToken runs the full pipeline for an inbound bearer
// token and returns a verified BrokerActor of CallerHumanOperator
// type on success.
func (v *GcloudIdentityTokenVerifier) VerifyBearerToken(jwtToken string) (domain.BrokerActor, error) {
	claims, err := v.signature.VerifyAndParse(jwtToken)
	if err != nil {
		return domain.BrokerActor{}, err
	}
	if claims.Issuer != v.issuer {
		return domain.BrokerActor{}, ErrIssuerMismatch
	}
	if v.emailAllowlist != nil {
		if _, ok := v.emailAllowlist[claims.Email]; !ok {
			return domain.BrokerActor{}, ErrEmailNotInAllowlist
		}
	}
	return domain.BrokerActor{
		Type:      domain.CallerHumanOperator,
		UserEmail: claims.Email,
	}, nil
}

// Sentinel errors raised by the gcloud identity verifier. Each
// failure mode is its own sentinel so the broker handler can
// distinguish authentication failure (signature / issuer) from
// authorisation failure (allowlist) for audit purposes.
var (
	ErrIssuerMismatch      = errors.New("gcloud identity: issuer is not the pinned Google STS")
	ErrEmailNotInAllowlist = errors.New("gcloud identity: caller email is not on the operator allowlist")
)
