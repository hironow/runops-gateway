package domain

import "errors"

// CallerType identifies the authentication path the broker used to
// admit the caller. The four types correspond to plan v8 §5.1's
// "4 caller authentication paths". The broker derives this value
// from the verified inbound credential — callers may NOT self-claim
// their type (plan v8 §5.4 request schema lockdown).
type CallerType string

const (
	CallerHumanOperator   CallerType = "human-operator"
	CallerGatewayService  CallerType = "gateway-service"
	CallerAIAgent         CallerType = "ai-agent"
	CallerWorkspaceDaemon CallerType = "workspace-daemon"
)

// AllCallerTypes returns every caller type in declaration order so
// matrix tests can iterate without enumerating manually.
func AllCallerTypes() []CallerType {
	return []CallerType{
		CallerHumanOperator,
		CallerGatewayService,
		CallerAIAgent,
		CallerWorkspaceDaemon,
	}
}

// Tool identifies one of the five runops tools. The broker mints a
// token scoped to exactly one Tool per request — multi-tool tokens
// are forbidden (plan v8 §5.3).
type Tool string

const (
	ToolPaintress Tool = "paintress"
	ToolSightjack Tool = "sightjack"
	ToolAmadeus   Tool = "amadeus"
	ToolDominator Tool = "dominator"
	ToolPhonewave Tool = "phonewave"
)

// PermissionLevel is the GitHub repository permission level the broker
// requests when minting an installation token. The broker only ever
// asks for read or write — admin-level permissions are not part of
// any tool's grant in plan v8 §5.3.
type PermissionLevel string

const (
	PermNone  PermissionLevel = ""
	PermRead  PermissionLevel = "read"
	PermWrite PermissionLevel = "write"
)

// RepositoryPermissions mirrors GitHub's `permissions` object on the
// installation token mint API. Zero-value fields (PermNone) are
// omitted from the actual mint request — the broker asks ONLY for
// what plan v8 §5.3 grants.
type RepositoryPermissions struct {
	Contents     PermissionLevel
	PullRequests PermissionLevel
}

// GrantPolicy is the immutable matrix that maps (CallerType, Tool) to
// allow/deny + repository permissions. Phase 1 returns the default
// matrix from plan v8 §5.3; runtime configurability is intentionally
// NOT exposed (the matrix is auth_boundary and must travel through
// the release-gate as code).
type GrantPolicy struct{}

// DefaultGrantPolicy returns the canonical broker grant matrix.
func DefaultGrantPolicy() GrantPolicy {
	return GrantPolicy{}
}

// IsAllowed reports whether caller may request a token for tool.
// Returns ErrToolNotPermitted when the (caller, tool) pair is outside
// the matrix (e.g. tool=phonewave for any caller).
func (p GrantPolicy) IsAllowed(_ CallerType, tool Tool) error {
	if _, err := p.PermissionsFor(tool); err != nil {
		return err
	}
	return nil
}

// PermissionsFor returns the per-tool repository permission scope from
// plan v8 §5.3. Phonewave is rejected here too so callers cannot
// route around IsAllowed by inferring scope directly.
func (GrantPolicy) PermissionsFor(tool Tool) (RepositoryPermissions, error) {
	switch tool {
	case ToolPaintress:
		return RepositoryPermissions{Contents: PermWrite, PullRequests: PermWrite}, nil
	case ToolSightjack:
		return RepositoryPermissions{Contents: PermRead}, nil
	case ToolAmadeus:
		return RepositoryPermissions{Contents: PermRead, PullRequests: PermRead}, nil
	case ToolDominator:
		return RepositoryPermissions{Contents: PermRead}, nil
	case ToolPhonewave:
		return RepositoryPermissions{}, ErrToolNotPermitted
	default:
		return RepositoryPermissions{}, ErrToolNotPermitted
	}
}

// allowedRequestFields enumerates plan v8 §5.4 allow-list. Anything
// outside this set is rejected so the broker derives all permissions
// / repo / actor from the verified caller credential rather than from
// caller-supplied claims.
var allowedRequestFields = map[string]struct{}{
	"project_id": {},
	"tool":       {},
	"session_id": {},
}

// knownEscalationFields enumerates fields that, if present, signal a
// privilege-escalation attempt rather than a typo. They map 1:1 to
// the §5.4 禁止 fields list and are returned as 403 (audit'd as an
// attack attempt) rather than 400.
var knownEscalationFields = map[string]struct{}{
	"repo":            {},
	"repository":      {},
	"repositories":    {},
	"permissions":     {},
	"installation_id": {},
	"actor_type":      {},
	"actor":           {},
}

// ValidateBrokerRequest enforces plan v8 §5.4 request schema lockdown.
// Returns ErrRequestSchemaViolation (=> 403, audit) when a known
// escalation field is present, ErrUnknownRequestField (=> 400) for
// any other unrecognised field, and nil when the request body is
// strictly within the allow-list.
//
// Validation operates on a decoded map so the caller (HTTP handler)
// can json.Unmarshal into map[string]any first, then run this
// gate before constructing the typed request.
func ValidateBrokerRequest(raw map[string]any) error {
	for k := range raw {
		if _, ok := allowedRequestFields[k]; ok {
			continue
		}
		if _, ok := knownEscalationFields[k]; ok {
			return ErrRequestSchemaViolation
		}
		return ErrUnknownRequestField
	}
	return nil
}

// Sentinel errors raised by the broker grant matrix and request
// schema validation.
var (
	ErrToolNotPermitted       = errors.New("broker: tool not permitted for caller")
	ErrRequestSchemaViolation = errors.New("broker: request contains caller-supplied escalation field")
	ErrUnknownRequestField    = errors.New("broker: request contains unknown field")
)
