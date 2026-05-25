package auth

import (
	"errors"
	"net/http"
	"strings"

	"github.com/hironow/runops-gateway/internal/core/domain"
)

// ChainAuthenticator implements broker.Authenticator (via Go's
// structural typing) by dispatching the inbound request to one of
// the 4 caller-type verifiers based on the X-Broker-Caller-Type
// header (refs#0007 plan v8 §5.1).
//
// The dispatcher does NOT guess the caller type from the bearer
// alone. Each caller type has DIFFERENT downstream policy
// (operator allowlist / SA allowlist / agent session lookup), so
// guessing would defeat the per-caller boundary. Callers MUST
// declare their type explicitly via the header; the chain
// then trusts the verifier's signature path to authenticate the
// claim.
type ChainAuthenticator struct {
	humanOperator   *GcloudIdentityTokenVerifier
	gatewayService  *CloudRunIAMVerifier
	workspaceDaemon *WorkloadIdentityVerifier
	aiAgent         *DelegatedAgentVerifier
}

// NewChainAuthenticator wires the 4 verifiers. All four must be
// non-nil — production deployments always provide every caller
// type's verifier (the composition root in Phase 3b-3 fails
// startup if env vars for any caller type are missing).
func NewChainAuthenticator(
	humanOperator *GcloudIdentityTokenVerifier,
	gatewayService *CloudRunIAMVerifier,
	workspaceDaemon *WorkloadIdentityVerifier,
	aiAgent *DelegatedAgentVerifier,
) *ChainAuthenticator {
	return &ChainAuthenticator{
		humanOperator:   humanOperator,
		gatewayService:  gatewayService,
		workspaceDaemon: workspaceDaemon,
		aiAgent:         aiAgent,
	}
}

const (
	headerCallerType    = "X-Broker-Caller-Type"
	headerSessionID     = "X-Broker-Session-Id"
	headerAuthorization = "Authorization"
	bearerPrefix        = "Bearer "
)

// Authenticate runs the dispatch pipeline:
//
//  1. Extract Bearer JWT from Authorization header.
//  2. Read X-Broker-Caller-Type and route to the right verifier.
//  3. For ai-agent, also read X-Broker-Session-Id and pass
//     projectID + tool through to DelegatedAgentVerifier so the
//     pinned (SA, project, tool) triple is verified at the same
//     time as the bearer.
func (c *ChainAuthenticator) Authenticate(r *http.Request, projectID string, tool domain.Tool) (domain.BrokerActor, error) {
	bearer, err := extractBearer(r)
	if err != nil {
		return domain.BrokerActor{}, err
	}
	callerType := strings.TrimSpace(r.Header.Get(headerCallerType))
	if callerType == "" {
		return domain.BrokerActor{}, ErrCallerTypeMissing
	}
	switch domain.CallerType(callerType) {
	case domain.CallerHumanOperator:
		return c.humanOperator.VerifyBearerToken(bearer)
	case domain.CallerGatewayService:
		return c.gatewayService.VerifyBearerToken(bearer)
	case domain.CallerWorkspaceDaemon:
		return c.workspaceDaemon.VerifyBearerToken(bearer)
	case domain.CallerAIAgent:
		sessionID := strings.TrimSpace(r.Header.Get(headerSessionID))
		if sessionID == "" {
			return domain.BrokerActor{}, ErrAIAgentMissingSessionID
		}
		return c.aiAgent.VerifyAndResolve(r.Context(), bearer, sessionID, projectID, tool)
	default:
		return domain.BrokerActor{}, ErrCallerTypeUnknown
	}
}

// extractBearer pulls the JWT out of the Authorization header.
// Only RFC 6750 Bearer scheme is accepted; the leading whitespace
// trim guards against header-injection variants.
func extractBearer(r *http.Request) (string, error) {
	raw := r.Header.Get(headerAuthorization)
	if raw == "" {
		return "", ErrBearerMissing
	}
	if !strings.HasPrefix(raw, bearerPrefix) {
		return "", ErrBearerMalformed
	}
	bearer := strings.TrimSpace(strings.TrimPrefix(raw, bearerPrefix))
	if bearer == "" {
		return "", ErrBearerMalformed
	}
	return bearer, nil
}

// Sentinel errors raised by the chain dispatcher. Each maps to a
// distinct audit signal so the broker handler can render the
// right HTTP status + audit log message.
var (
	ErrBearerMissing           = errors.New("chain: Authorization header missing")
	ErrBearerMalformed         = errors.New("chain: Authorization header malformed (Bearer scheme required)")
	ErrCallerTypeMissing       = errors.New("chain: X-Broker-Caller-Type header missing")
	ErrCallerTypeUnknown       = errors.New("chain: X-Broker-Caller-Type value not recognised")
	ErrAIAgentMissingSessionID = errors.New("chain: ai-agent caller requires X-Broker-Session-Id header")
)
