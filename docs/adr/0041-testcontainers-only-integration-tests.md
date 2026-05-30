# 0041. Integration tests run only via testcontainers (no external emulator)

**Date:** 2026-05-31
**Status:** Accepted

## Context

Integration tests (Pub/Sub bridge in `tests/integration/`, Firestore registry in
`internal/adapter/output/state/`) connected to a firebase emulator that was
started **externally**: `docker compose up -d pubsub-emulator` (locally via
`just pubsub-up`, in CI via a dedicated workflow step), with topics created by
`scripts/init-pubsub.sh`, and the tests reaching it through a fixed
`PUBSUB_EMULATOR_HOST` / `FIRESTORE_EMULATOR_HOST`.

Two failure modes followed from this external coupling:

- **Silent skip**: `requireEmulator(t)` / `newFirestoreTest` only checked
  whether the env var was *set*. With it unset, the test `t.Skip`-ped — so
  forgetting `just pubsub-up` made `go test` pass green while verifying nothing.
- **False-positive hang**: with the env var set but the emulator actually down
  (observed repeatedly when the local Docker daemon — OrbStack — crashed mid
  session on 2026-05-31), the test did not skip; it tried to connect and hung
  until timeout. The test's correctness depended on out-of-band local state.

The CI workflow likewise depended on a docker-compose step running before the
test step, so the test was never self-describing about its own prerequisites.

## Decision

**Integration tests depend ONLY on testcontainers and run entirely inside the
container testcontainers starts.** They never rely on `just pubsub-up`, an
externally-set `PUBSUB_EMULATOR_HOST` / `FIRESTORE_EMULATOR_HOST`, a
docker-compose-started emulator, or `scripts/init-*.sh`.

- Each integration test package has a `TestMain` that calls
  `testutils.RunFirebaseEmulator`, which builds the emulator image from the repo
  `docker/firebase-emulator/Dockerfile` (`FromDockerfile`, `KeepImage`) — so the
  test does not even depend on an external image registry — exposes Pub/Sub +
  Firestore on dynamic ports via `MappedPort`, injects them into the emulator
  env vars, initializes topics (`testutils.InitPubSub`, the Go port of
  `init-pubsub.sh` with a 9399 readiness retry), runs the tests, and
  `Terminate`s the container.
- `requireEmulator` / `envOr` / the Firestore skip / per-file message adapters
  are removed. Shared helpers live in `tests/utils/` (the only importable test
  utility location): `RunFirebaseEmulator`, `InitPubSub`, `MsgAdapter`,
  `ReceiveOne`, and the topic/sub/project constants.
- `compose.yaml`'s `pubsub-emulator` service, the `just pubsub-up/down/init` and
  `firestore-up/down/init` recipes, and `scripts/init-pubsub.sh` /
  `init-firestore.sh` are deleted. `docker/firebase-emulator/Dockerfile` stays —
  it is now consumed only by testcontainers. The Jaeger service in
  `compose.yaml` stays (local OTel trace backend, unrelated to integration
  tests).
- `testcontainers-go` is a **test-only dependency** (`//go:build integration`);
  the production binaries (`cmd/server`, `dmail-receiver`, `dmail-emitter`,
  `runops`) do not link it, so binary size and runtime deps are unchanged.

`testcontainers-go/modules/gcloud` is **not** adopted: it runs Pub/Sub and
Firestore as separate containers and does not support the repo's custom
firebase image, conflicting with the single-image-both-emulators setup.

## Enforcement inventory

### Entry points

- `tests/integration/setup_test.go` `TestMain` — Pub/Sub bridge tests.
- `internal/adapter/output/state/setup_integration_test.go` `TestMain` —
  Firestore registry tests.
- `.github/workflows/ci.yaml` `integration` job — runs both with `go test
  -tags=integration`, no docker compose step.

### Persistent / carried data needed at each enforcement point

- Container handle + `MappedPort("9399/tcp")` / `MappedPort("8080/tcp")` →
  `PUBSUB_EMULATOR_HOST` / `FIRESTORE_EMULATOR_HOST` set by TestMain.
- `GOOGLE_CLOUD_PROJECT` / project id from `testutils.FirebaseProjectID`.
- Topic/sub names from `tests/utils` constants (no hardcoded literals in tests).

### Bypass candidates ("where can this go wrong?")

1. A test re-introduces `requireEmulator` / `t.Skip` on a missing env var →
   silent skip returns.
2. A test reads `os.Getenv("PUBSUB_EMULATOR_HOST")` / hardcodes
   `localhost:9399` instead of using the TestMain-injected dynamic port.
3. CI or a justfile recipe re-adds `docker compose up pubsub-emulator` so tests
   secretly rely on an external emulator again.
4. The emulator's 4400 hub answers before the 9399/8080 listeners are ready →
   flaky first RPC (mitigated by `InitPubSub`'s readiness retry).

### Tests proving coverage (one per enforcement point)

- TestMain in each package starts the container and fails loudly (os.Exit(1))
  when Docker is unavailable — no skip path exists.
- `grep -rn requireEmulator tests/` and `grep -rn 'PUBSUB_EMULATOR_HOST=localhost'`
  return zero (CI guard candidate / manual gate).
- ci.yaml `integration` job contains no `docker compose` invocation; the test
  step is the sole emulator entry point.
- `InitPubSub` retries the admin connection (NotFound = reachable) for 60s.

## Consequences

### Positive

- Tests are self-contained: `go test -tags=integration ./tests/integration/...
  ./internal/adapter/output/state/...` works with nothing started beforehand.
- Silent skip is eliminated — a missing emulator is a hard failure, not a pass.
- CI and local runs are identical (same TestMain, same image build).
- One CI job instead of two; no compose build/up/stop/init steps.

### Negative

- A running Docker daemon is now mandatory for integration tests (testcontainers
  cannot start a container without it). Daemon crashes fail loudly rather than
  silently skipping — better, but still a hard dependency.
- `testcontainers-go` adds ~250 lines of indirect deps to `go.sum` (docker
  client etc.), test-only.
- First run builds the firebase image (30–60s); `KeepImage` amortizes
  subsequent runs.

### Neutral

- `compose.yaml`'s emulator is gone; ad-hoc manual `cmd/server`-against-emulator
  smoke is folded into the integration tests rather than a long-running compose
  service.
- ADR 0024 (tofu test / pytest split) is orthogonal — it concerns Terraform IaC
  tests, not Go integration tests.
