# 0032. Token broker caller grant matrix and per-project repo binding

**Date:** 2026-05-08
**Status:** Accepted

## Context

The runops-gateway needs to mint short-lived GitHub installation
tokens on behalf of 4 caller types (human-operator / gateway-service
/ ai-agent / workspace-daemon) for 5 tools (paintress / sightjack /
amadeus / dominator / phonewave). The token broker endpoint
(refs#0007 plan v8 §6) is the single mint surface for the entire
fleet; once it ships, every code path that wants GitHub access
flows through this matrix.

Without a matrix pinned in code review territory:

- A future tool may inherit broader permissions than its actual
  workflow requires (= privilege creep).
- A future caller type may bypass per-tool deny rules — most
  importantly the phonewave deny, which exists because phonewave
  is a transport-only D-Mail courier with no business touching
  GitHub.
- Per-project repo binding (= one project's installation token
  must scope to exactly one repository, never paintress-global or
  multi-repo) may be silently loosened by a future "convenience"
  parameter on the mint API.
- Archived / paused / deleted projects may continue to mint
  tokens long after their lifecycle has stopped, because the
  registry stays populated even when Status is no longer
  `active`.

Plan v8 §5.3 captured these invariants in the implementation plan,
but plans are mutable. ADRs are not. This decision pins the
matrix at the architectural layer.

## Decision

### Grant matrix (caller × tool → permission)

| Tool | Repository permission | Why this scope |
|---|---|---|
| paintress | `contents:write` + `pull_requests:write` | Expedition commits + PR open |
| sightjack | `contents:read` | wave audit reads only |
| amadeus | `contents:read` + `pull_requests:read` | PR review / convergence reads |
| dominator | `contents:read` | NFR judgment reads only |
| **phonewave** | **— (token mint denied for ALL caller types)** | Transport-only courier; no GitHub touch is legitimate |

The matrix is enforced in 3 layers (defence-in-depth):

1. `domain.GrantPolicy.IsAllowed` and `PermissionsFor` — pure
   functions, unit-tested.
2. `usecase.BrokerTokenService.Mint` — re-checks the matrix before
   reaching the upstream mint adapter, so a future refactor that
   bypasses the use case still fails closed.
3. `internal/adapter/output/github/installation_token_broker.go`
   — re-checks again at the upstream API boundary, so a direct
   adapter caller still sees the same matrix.

Adding a new tool requires:

- editing `domain.GrantPolicy.PermissionsFor`,
- updating this ADR (or, if the change is incompatible with the
  philosophy of "minimal per-tool scope", superseding this ADR
  with a new one),
- updating the test grid that iterates `domain.AllCallerTypes()`
  × every tool.

This deliberate friction is the point.

### Per-project repository binding

Every installation token mint MUST request EXACTLY ONE repository
— the repo bound to `project_id` in the project registry. The
installation token API's `Repositories` parameter MUST contain a
single entry. Multi-repo / cross-project / paintress-global
tokens are FORBIDDEN.

This is enforced by:

- `internal/adapter/output/github/installation_token_broker.go`
  building the `Repositories` slice from `project.GitHubRepo`
  alone,
- the per-project test
  (`TestInstallationTokenBroker_Mint_PerProjectRepoBindingOnlyOneRepo`)
  asserting `len(Repositories) == 1`.

A project_id whose registry record carries `installation_id == 0`
(= no GitHub App installation bound) is rejected with
`usecase.ErrProjectInstallationMissing` at the use case layer
before the broker is consulted.

### Project lifecycle gate

`project.Status` MUST be `active` at mint time. Archived /
paused / deleted projects are rejected with
`usecase.ErrProjectNotActive` regardless of every other gate.
The registry round-trip at mint time is intentional — lifecycle
event visibility cannot be deferred to an in-process cache. (A
short-TTL read-through token cache exists, but it caches the
*minted token*, not the project status.)

### Request schema lockdown

The HTTP handler accepts ONLY these caller-supplied fields:

- `project_id`
- `tool`
- `session_id` (AI agent only)

All permissions / installation_id / actor_type are derived by the
broker from the verified caller credential. Caller-supplied
escalation fields trigger an audit-shaped 403:

- `repo` / `repository` / `repositories`
- `permissions`
- `installation_id`
- `actor_type` / `actor.user_email` / `actor`

The handler tags the audit log with `audit_event=broker_escalation_attempt`
so every escalation attempt is observable.

### Token leakage policy

The minted token leaves the broker through EXACTLY ONE channel:
the HTTP response body. It MUST NOT appear in:

- structured logs / log fields,
- OTel span attributes,
- D-Mail message bodies / metadata,
- Pub/Sub message attributes / payloads,
- archive directories or any local-disk artifact.

The only token-derived value permitted on audit surfaces is
`audit_fingerprint = sha256(token)[:8] hex`
(`domain.AuditFingerprint`). The Semgrep rule set under
`.semgrep/rules/release-gate/gateway-broker-token-leak.yaml`
mechanically enforces this at code-review time.

## Consequences

### Positive

- Code review can mechanically check the matrix against new tool
  additions.
- Phonewave deny is structural (cannot be softened by config or
  by a single-line code change without an ADR amend).
- Per-project binding is enforced in 3 layers + this ADR pin.
- Caller-supplied escalation attempts are surfaced via dedicated
  audit signal rather than mixing with caller-bug 400s.

### Negative

- Adding a new tool requires both a code edit + this ADR's matrix
  to update (= friction). For experimental tools that don't yet
  warrant a permanent matrix entry, the operator's only options
  are (a) deny by default (= no broker access) or (b) accept the
  ADR friction. There is no "experimental" tier.
- Per-tool permission narrowing means downstream tools that
  legitimately need a permission absent from the matrix (e.g. a
  future amadeus extension that needs `issues:write` to manage
  issue assignments) cannot work without an ADR update first.

### Neutral

- The matrix is a closed set. Tools not in this matrix cannot
  receive tokens; this is interpreted as "the broker is the
  documentation of what tools exist", not as a limitation.
- Future amendments must be additive — removing or narrowing an
  existing entry is a behaviour change that callers may already
  depend on, and warrants a superseding ADR rather than a quiet
  edit.
