# 0042. Local trace backend is the shared dotfiles `tel` stack (drop per-repo Jaeger)

**Date:** 2026-05-31
**Status:** Accepted

## Context

ADR 0041 (testcontainers-only integration tests) deleted `compose.yaml`'s
`pubsub-emulator` service and the `pubsub-up/down/init` recipes, but explicitly
kept the **Jaeger** service "local OTel trace backend, unrelated to integration
tests". That left `compose.yaml` with exactly one service (Jaeger v2,
`runops-jaeger`, OTLP `:4317`/`:4318`, UI `:16686`) and the `trace-up` /
`trace-down` / `trace-view` recipes — the only thing runops-gateway still starts
locally.

In parallel, the developer environment is standardised on the **dotfiles**
repository, which vendors a shared local-dev stack:

- `emu` (`emulator/compose.yaml`) — firebase emulator suite: Pub/Sub `:9399`,
  Firestore `:8080`, etc. `just emu-up-only firebase-emulator` starts just that.
- `tel` (`telemetry/compose.yaml`) — OpenTelemetry Collector on `:4317`/`:4318`
  feeding Tempo (traces) + Loki (logs) + Prometheus (metrics), viewed in Grafana.

Running runops' own Jaeger **alongside** the dotfiles `tel` stack is actively
harmful: both bind host `:4317`/`:4318`, so the second `up` either fails to bind
or the two fight over the port; and a second OTLP backend double-spends memory
under the OrbStack VM cap (this contributed to the 2026-05-31 OrbStack VM crash
when `emu` + `tel` + a runops backend ran together). The per-repo Jaeger
duplicates a richer capability the shared stack already provides.

The ideal end state: **runops-gateway starts nothing locally.** A developer
clones dotfiles, starts only what they need from it, and points runops binaries
at those shared endpoints.

## Decision

**The local OpenTelemetry trace backend is the shared dotfiles `tel` stack
(otel-collector on `:4317`), not a per-repo Jaeger.**

- Delete `compose.yaml` (Jaeger was its only remaining service after ADR 0041).
- Delete the `trace-up` / `trace-down` / `trace-view` justfile recipes.
- Local tracing: from a dotfiles checkout run `just tel-up`, then start binaries
  with `OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4317`. Traces are viewed in
  Grafana/Tempo (dotfiles) instead of the Jaeger UI.
- Manual end-to-end smoke against real emulators: `just emu-up-only
  firebase-emulator` (dotfiles) for Pub/Sub `:9399` + Firestore `:8080`, plus
  `just tel-up` for OTLP. Automated integration tests need none of this — they
  use testcontainers (ADR 0041) and require only a running Docker daemon.

**ADR 0020's core decision is unchanged.** The app still does *direct OTLP gRPC
export* to `OTEL_EXPORTER_OTLP_ENDPOINT` (no Collector sidecar in the app's own
process / deployment). Only the dev-environment backend *behind* `localhost:4317`
moves from a per-repo Jaeger all-in-one to the shared dotfiles otel-collector.
ADR 0020 rejected a Collector for the **production** topology (Cloud Run sidecar
/ VM-shared collector adding failure domains + cold-start budget); that rejection
is about the app's own deployment and is untouched here — an externally-provided
dev collector does not reintroduce that production risk.

This **supersedes the single sub-point** in ADR 0041 that said "the Jaeger
service in `compose.yaml` stays"; the rest of ADR 0041 stands. It **refines** the
local-backend implementation bullet of ADR 0020 (which had said "`compose.yaml`
に Jaeger サービスを追加し `just trace-up/down/view`"); ADR 0020's case-A export
decision is otherwise intact.

## Enforcement inventory

### Entry points

- `justfile` — no `trace-up`/`trace-down`/`trace-view`; tracing guidance is a
  comment pointing at dotfiles `tel`.
- Repo root — no `compose.yaml`; runops-gateway has no compose-started service.
- `.github/workflows/ci.yaml` — no Jaeger / `docker compose` for tracing (CI
  never needed a trace backend).

### Persistent / carried data needed at each enforcement point

- `OTEL_EXPORTER_OTLP_ENDPOINT` (env) — the only knob; `http://localhost:4317`
  locally (dotfiles `tel`), `telemetry.googleapis.com:443` in prod (ADR 0020).
- `OTEL_SERVICE_NAME` per binary (unchanged from ADR 0020).

### Bypass candidates ("where can this go wrong?")

1. Someone re-adds a `compose.yaml` with a Jaeger (or any) service and a
   `trace-up` recipe → runops starts local infra again and re-collides on
   `:4317` with dotfiles `tel`.
2. A binary hardcodes a Jaeger/OTLP endpoint instead of reading
   `OTEL_EXPORTER_OTLP_ENDPOINT` → can't be repointed at the shared collector.
3. Docs regress to "`just trace-up`" so a developer starts a duplicate backend.

### Tests proving coverage

- `test -e compose.yaml` is false; `grep -rn "trace-up\|runops-jaeger" justfile`
  returns zero (manual gate / CI guard candidate).
- `grep -rn "OTEL_EXPORTER_OTLP_ENDPOINT" internal/ cmd/` confirms binaries read
  the env var rather than a literal endpoint (unchanged from ADR 0020).
- `docs/local-verification.md` documents the dotfiles `tel` reuse path, not a
  local Jaeger.

## Consequences

### Positive

- runops-gateway starts **nothing** locally; the host-port `:4317`/`:4318`
  collision with dotfiles `tel` is structurally impossible.
- Lower local memory pressure (one OTLP backend, shared) — directly mitigates the
  OrbStack VM-cap crash.
- Richer local observability: Grafana/Tempo + Loki + Prometheus instead of just
  the Jaeger trace UI.
- One source of truth for the local dev stack (dotfiles); runops only documents
  which dotfiles commands to run.

### Negative

- Local tracing now requires a dotfiles checkout and `just tel-up`; it is no
  longer a single in-repo command.
- The Jaeger UI is gone — trace exploration moves to Grafana/Tempo (different UX).
- A developer who does not use the dotfiles stack must point
  `OTEL_EXPORTER_OTLP_ENDPOINT` at their own OTLP backend (or leave it unset; the
  SDK falls back to a no-op exporter per ADR 0020).

### Neutral

- Production tracing is unchanged (direct OTLP to Cloud Trace, ADR 0020).
- `docker/firebase-emulator/Dockerfile` is unaffected (consumed by testcontainers
  per ADR 0041).
- The `OTEL_EXPORTER_OTLP_ENDPOINT` default for local manual runs is documented,
  not baked into code.
