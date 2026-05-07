package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"

	"github.com/hironow/runops-gateway/internal/core/domain"
)

// JWKSVerifier verifies the RS256 signature of a JWT against a JWKS
// endpoint and then validates the claim's audience + expiry against
// the broker's pinned audience (refs#0007 plan v8 §5.1).
//
// The signature path uses `MicahParks/keyfunc/v3` which:
//
//   - fetches and caches the JWKS document at construction time,
//   - returns a `jwt.Keyfunc` that maps the JWT header `kid` to the
//     correct RSA public key (= structural defence against forged
//     tokens whose `kid` references a key the JWKS does not list),
//   - rotates keys automatically when the JWKS endpoint serves a
//     newer document.
//
// The signing-method allowlist (`jwt.WithValidMethods({"RS256"})`)
// blocks the `alg=none` attack class; the test
// `TestJWKSVerifier_VerifyAndParse_AlgNoneAttackRejected` is a
// regression guard so a future refactor cannot quietly drop it.
//
// Phase 2d-2c/d/e plug this verifier into the 3 remaining caller
// types (gcloud_identity / cloudrun_iam / workload_identity); each
// will construct a JWKSVerifier with its issuer's JWKS URL +
// audience and route requests by issuer.
type JWKSVerifier struct {
	keyfunc  keyfunc.Keyfunc
	audience string
	now      func() time.Time
}

// NewJWKSVerifier fetches the JWKS document once at construction and
// keeps the keyfunc.Keyfunc handle for subsequent verifications.
// keyfunc.NewDefaultCtx handles caching + background refresh so
// callers don't need to manage lifecycles.
func NewJWKSVerifier(ctx context.Context, jwksURL, audience string, now func() time.Time) (*JWKSVerifier, error) {
	if now == nil {
		now = time.Now
	}
	kf, err := keyfunc.NewDefaultCtx(ctx, []string{jwksURL})
	if err != nil {
		return nil, fmt.Errorf("jwks: NewDefaultCtx: %w", err)
	}
	return &JWKSVerifier{keyfunc: kf, audience: audience, now: now}, nil
}

// ErrJWKSTokenInvalid is returned when keyfunc verifies the JWT but
// jwt.Token.Valid is false (= a non-signature reason). This is
// distinct from the domain-level claim sentinels.
var ErrJWKSTokenInvalid = errors.New("jwks: token reported invalid by jwt parser")

// VerifyAndParse runs the full pipeline: signature verify
// (RS256 only, keyfunc-resolved kid) → domain.ParseIdentityClaims
// → domain.IdentityClaims.Validate(now, audience). On success the
// caller may trust every field on the returned IdentityClaims.
func (v *JWKSVerifier) VerifyAndParse(jwtToken string) (domain.IdentityClaims, error) {
	parsed, err := jwt.Parse(jwtToken, v.keyfunc.Keyfunc, jwt.WithValidMethods([]string{"RS256"}))
	if err != nil {
		return domain.IdentityClaims{}, fmt.Errorf("jwks: verify: %w", err)
	}
	if !parsed.Valid {
		return domain.IdentityClaims{}, ErrJWKSTokenInvalid
	}
	claims, err := domain.ParseIdentityClaims(jwtToken)
	if err != nil {
		return domain.IdentityClaims{}, err
	}
	if err := claims.Validate(v.now(), v.audience); err != nil {
		return domain.IdentityClaims{}, err
	}
	return claims, nil
}
