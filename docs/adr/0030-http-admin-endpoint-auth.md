# 0030. HTTP admin endpoint authentication

**Date:** 2026-05-07
**Status:** Accepted

## Context

ADR 0025 promised that production registry mutations would flow
through a "gateway HTTP admin endpoint", separating operator-on-laptop
SQLite use from the multi-instance Cloud Run path. Issue #0012
delivers that endpoint; this ADR pins the auth contract so future
edits do not soften the security boundary by accident.

The gateway has no existing admin auth pattern: Slack uses HMAC-signed
webhooks (ADR 0016) and the runops CLI talks to the registry directly.
Operators reaching `/admin/projects` are humans (with curl, Postman, or
deploy scripts) and AI agents acting on their behalf, not Slack.
Whatever we choose has to:

1. Be simple enough that an operator's day-1 cutover does not stall on
   client-side signing libraries.
2. Be opt-in — non-multiplex deployments never receive the route, even
   if they accidentally inherit the new env var.
3. Not leak the token through log lines, error responses, or OTel
   spans, ever, even when the auth itself fails.

## Decision

### Bearer-token auth, env-driven, opt-in

`Authorization: Bearer <RUNOPS_ADMIN_TOKEN>` is the only accepted
credential. Verification is done in constant time
(`crypto/subtle.ConstantTimeCompare`).

In production the token lives in Secret Manager and is injected into
the Cloud Run service's env via terraform; rotation is a Secret
Manager update plus a Cloud Run revision bump. The static-token shape
is deliberate — operator-friendly, scripts and `curl` work without
extra tooling, and the surface is small enough that we do not need
HMAC payload signing for Phase α.

The handler is registered **only** when both the project registry is
wired (env-driven, ADR 0026) **and** `RUNOPS_ADMIN_TOKEN` is set. Either
side missing leaves the routes unreachable; this is the "opt-in
fail-closed" behaviour we expect from any new boundary.

### §4: Strict Bearer header parsing

To prevent the class of bugs where "Bearer abc\n" would be silently
accepted because of a `strings.TrimSpace` upstream, the handler
implements a tight grammar:

- Read `r.Header.Get("Authorization")` raw — **no TrimSpace anywhere**.
- Require `len(raw) >= 7`.
- Require `EqualFold(raw[:6], "Bearer")` — `Bearer` / `bearer` /
  `BEARER` accepted, but the token itself stays case-sensitive.
- Require `raw[6] == ' '` — exactly one space separator.
- Take `claimed := raw[7:]`, reject if empty.
- Reject if `claimed` contains any whitespace (`unicode.IsSpace`) or
  control character (`unicode.IsControl`). This is the catch-all for
  trailing newlines, leading whitespace inside the token, double-space
  separators, and pasted-in tabs.
- Compare in constant time against the configured token.

Deviations are tested at the `handler_test.go::TestHandler_Authorize_TokenNormalization`
boundary so any future loosening of the rule fails CI loudly.

### Token must never appear in logs, responses, or spans

When auth fails the handler emits a constant `slog.Warn("admin: auth
failed", "endpoint", path)` — never the received header, never the
configured token, never any substring of either. The HTTP response
body is `{"error":"unauthorized"}` (a fixed string). OTel spans only
record `auth.failed=true`. `Handler.String()` returns
`"admin.Handler{token=<redacted>}"` so accidental `%v` / `Sprintf`
through structured logging cannot leak it. A unit test
(`TestHandler_Authorize_NeverLogsToken`) captures `slog.Default()`
output and asserts that the token literal does not appear.

### Endpoints

```
POST /admin/projects                  — Add (Body: Project JSON)
GET  /admin/projects?status=...       — List (filter: active|archived|all)
GET  /admin/projects/{id}             — Get
POST /admin/projects/{id}/archive     — Archive (idempotent)
```

`Add` ignores client-supplied `status` / `created_at` / `archived_at` —
the server forces `Status=active`, `CreatedAt=now`, `ArchivedAt=nil`.
`List` returns `{"projects":[...]}` with the slice initialised so empty
results render `[]` instead of `null`. Sentinel errors map to HTTP
status:

- `domain.ErrInvalidProjectID` → 400
- `domain.ErrProjectNotFound` → 404
- `domain.ErrProjectAlreadyExists` → 409
- everything else → 500 (with the underlying error logged at error
  level, never echoed to the client)

### Tests run on every push (no build tag)

Both `handler_test.go` (in-memory fake registry) and `lifecycle_test.go`
(SQLite t.TempDir round-trip) live in the same package as the handler.
Neither uses `//go:build integration`, so the existing CI `Test` job
covers them under `-race` automatically. The path-derived audit trail
for #0007 / #0006 still routes through pubsub-integration; admin only
needs the in-process gate.

## Consequences

### Positive

- Operators have a simple cutover path: set `RUNOPS_ADMIN_TOKEN`, hit
  the endpoint with `curl -H "Authorization: Bearer ..."`. No client
  signing libraries, no clock-skew edge cases like Slack's signed
  webhooks have.
- Three layers protect the token: opt-in registration, redacted
  `String()`, log-free auth failures. A misbehaving log statement
  cannot leak it without one of the three breaking.
- `lifecycle_test.go` exercises the full HTTP → handler → SQLite
  registry → SQLite migration path on every push, so the cutover
  scenario stays green without operator-driven rituals.

### Negative

- Static tokens require manual rotation. Mitigated by Secret Manager
  versioning + Cloud Run revision bump, but it is not zero-touch.
- Four endpoints, no GUI. Operators script around `curl`; a human-
  friendly CLI driver lives in cmd/runops for dev, and a future
  `--remote` flag can extend it (out of scope here).
- Bearer auth alone does not authenticate the operator's identity;
  audit trails currently identify the **token** as actor. When IAP /
  identity binding lands, this ADR will be superseded.

### Neutral

- Per-OS list separator concerns from #0007 do not apply here — admin
  uses a single env var (the token) and JSON over HTTP.
- The `subtle.ConstantTimeCompare` check requires both arguments to
  be the same length, but Go's implementation handles unequal lengths
  by returning 0 without leaking length, so brute-forcing length
  remains hard.

## Out of scope

- **IAM Identity-Aware Proxy / SSO** — A future ADR can replace the
  static Bearer with Google identity once the operator's tooling is
  ready. The handler interface (`Register(mux)`) and the env-driven
  opt-in pattern carry over unchanged.
- **Rate limiting / brute-force protection** — Production deploys can
  layer Cloud Armor or Cloud Run concurrency caps in front of the
  endpoint; we do not implement application-level rate limiting in
  this PR.
- **Pagination** — Phase α has < 100 projects; full enumeration is
  fine. A `?cursor=...` parameter is straightforward to add later.
- **Audit log to a dedicated table** — current OTel spans suffice for
  Phase α triage; a dedicated audit store is a future enhancement.
- **`--remote=<url>` flag for cmd/runops** — admin server is shipped
  here; the client-side companion lives in a follow-up issue.

## Cutover procedure

1. tofu adds `RUNOPS_ADMIN_TOKEN` Secret Manager entry + Cloud Run
   env var binding.
2. Operator sets `RUNOPS_PROJECT_REGISTRY=firestore` /
   `RUNOPS_FIRESTORE_DATABASE=runops-registry` per ADR 0026 (already
   landed).
3. Operator deploys a new Cloud Run revision; boot logs print
   `admin endpoint registered (#0012)`.
4. Operator calls `curl -X POST /admin/projects -H "Authorization:
   Bearer ..."` to seed Firestore from the operator-local SQLite
   export (per ADR 0026 Cutover §Step 4); existing CLI now becomes
   dev-only.
5. Older Cloud Run deployments without the env / token continue to
   serve only Slack handlers; admin routes return 404.

## References

- ADR 0016 — Slack request timestamp freshness (auth pattern context)
- ADR 0025 — port/adapter dual strategy (HTTP admin endpoint promise)
- ADR 0026 — Firestore production deploy
- ADR 0027 — project_id metadata carry standard
- ADR 0028 / 0029 — receiver / emitter routing
- Issue #0012 (this PR), #0009 / #0011 (registry SoT)
- `internal/adapter/input/admin/handler.go` — handler + auth
- `internal/adapter/input/admin/handler_test.go` — auth + endpoint
  unit tests (incl. `TestHandler_Authorize_TokenNormalization` and
  `TestHandler_Authorize_NeverLogsToken`)
- `internal/adapter/input/admin/lifecycle_test.go` — SQLite registry
  round-trip
- `cmd/server/main.go` — opt-in wiring
