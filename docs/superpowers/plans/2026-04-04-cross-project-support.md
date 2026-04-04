# Cross-Project GCP Operations Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Enable runops-gateway to operate on GCP resources in any project, not just the gateway's own project.

**Architecture:** Add `project` and `location` fields to the button value JSON (notify-slack.sh), domain model (ApprovalRequest), and GCP Controller interface. Each button becomes self-contained with its target project/location. The Controller no longer holds a fixed Config — it receives project/location per-call.

**Tech Stack:** Go 1.26, bash (notify-slack.sh), jq, Cloud Run API v2, Cloud SQL Admin API

**Spec:** `docs/superpowers/specs/2026-04-04-cross-project-support-design.md`

---

## Tasks

### Task 1: Domain Model — Add Project and Location to ApprovalRequest

**Files:**

- Modify: `internal/core/domain/domain.go:76-104`
- Modify: `internal/core/domain/domain_test.go`

- [ ] **Step 1: Add fields to ApprovalRequest**

In `internal/core/domain/domain.go`, add `Project` and `Location` as the first two fields of `ApprovalRequest`:

```go
type ApprovalRequest struct {
 // Project is the GCP project ID of the target resource (e.g. "trade-non").
 Project string
 // Location is the GCP region of the target resource (e.g. "asia-northeast1").
 Location string
 // ResourceType is the kind of GCP resource to operate on.
 ResourceType ResourceType
 // ... rest unchanged
```

- [ ] **Step 2: Update domain_test.go fixtures**

Add `Project: "test-project"` and `Location: "asia-northeast1"` to all `ApprovalRequest` literals in `internal/core/domain/domain_test.go`.

- [ ] **Step 3: Run domain tests**

Run: `go test ./internal/core/domain/... -v`
Expected: PASS (domain tests don't validate these fields, just compile check)

- [ ] **Step 4: Commit**

```bash
git add internal/core/domain/
git commit -m "feat: add Project and Location fields to ApprovalRequest"
```

---

### Task 2: Port Interface — Update GCPController and OperationKey

**Files:**

- Modify: `internal/core/port/port.go:22-32,75-79`
- Modify: `internal/core/port/port_test.go`

- [ ] **Step 1: Update GCPController interface**

In `internal/core/port/port.go`, change the interface:

```go
type GCPController interface {
 ShiftTraffic(ctx context.Context, project, location, serviceName, revision string, percent int32) error
 ExecuteJob(ctx context.Context, project, location, jobName string, args []string) error
 TriggerBackup(ctx context.Context, project, instanceName string) error
 UpdateWorkerPool(ctx context.Context, project, location, poolName, revision string, percent int32) error
}
```

- [ ] **Step 2: Update OperationKey to include Project**

In the same file, update `OperationKey`:

```go
func OperationKey(req domain.ApprovalRequest) string {
 return fmt.Sprintf("%s/%s/%s/%s/%d",
  req.Project, req.ResourceType, req.ResourceNames, req.Action, req.IssuedAt)
}
```

- [ ] **Step 3: Update port_test.go stub**

In `internal/core/port/port_test.go`, update `stubGCPController` methods:

```go
func (s *stubGCPController) ShiftTraffic(_ context.Context, _, _, _, _ string, _ int32) error {
 return nil
}
func (s *stubGCPController) ExecuteJob(_ context.Context, _, _, _ string, _ []string) error {
 return nil
}
func (s *stubGCPController) TriggerBackup(_ context.Context, _, _ string) error {
 return nil
}
func (s *stubGCPController) UpdateWorkerPool(_ context.Context, _, _, _, _ string, _ int32) error {
 return nil
}
```

Also update the `OperationKey` test (if any) to include `Project` in the expected key format.

- [ ] **Step 4: Run port tests**

Run: `go test ./internal/core/port/... -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/core/port/
git commit -m "feat: add project/location to GCPController interface and OperationKey"
```

---

### Task 3: GCP Controller — Remove Config, Accept project/location per call

**Files:**

- Modify: `internal/adapter/output/gcp/controller.go`
- Modify: `internal/adapter/output/gcp/controller_test.go`

- [ ] **Step 1: Rewrite controller.go**

Remove `Config` struct, `NewController` constructor, and `Location()` method. Replace with a simple zero-value struct. Each method takes `project` and `location` as arguments:

```go
type Controller struct{}

func NewController() *Controller {
 return &Controller{}
}

func (c *Controller) ShiftTraffic(ctx context.Context, project, location, serviceName, revision string, percent int32) error {
 slog.InfoContext(ctx, "gcp: shifting traffic",
  "project", project, "service", serviceName, "revision", revision, "percent", percent)
 client, err := run.NewServicesClient(ctx)
 if err != nil {
  return fmt.Errorf("gcp: create services client: %w", err)
 }
 defer client.Close()
 servicePath := fmt.Sprintf("projects/%s/locations/%s/services/%s", project, location, serviceName)
 // ... rest of method unchanged except using project/location args
```

Apply the same pattern to `ExecuteJob` (use `project`, `location`), `UpdateWorkerPool` (use `project`, `location`), and `TriggerBackup` (use `project` only).

- [ ] **Step 2: Update controller_test.go**

Remove all `Config`-based tests. Update method call signatures:

```go
func TestController_ImplementsInterface(t *testing.T) {
 var _ port.GCPController = (*gcp.Controller)(nil)
}

func TestShiftTraffic_ReturnsError_WhenAPIFails(t *testing.T) {
 ctrl := gcp.NewController()
 err := ctrl.ShiftTraffic(ctx, "test-project", "asia-northeast1", "my-service", "my-revision", 10)
 // ... existing error assertion
}
```

Update all 4 method test calls similarly.

- [ ] **Step 3: Run controller tests**

Run: `go test ./internal/adapter/output/gcp/... -v`
Expected: PASS (these tests call real GCP APIs that fail — they test error paths)

- [ ] **Step 4: Commit**

```bash
git add internal/adapter/output/gcp/
git commit -m "refactor: remove Config from GCP Controller, accept project/location per call"
```

---

### Task 4: Usecase — Pass project/location to GCP Controller

**Files:**

- Modify: `internal/usecase/runops.go`
- Modify: `internal/usecase/runops_test.go`

- [ ] **Step 1: Update mock GCPController in runops_test.go**

Update `mockGCP` to capture project/location:

```go
type gcpCall struct {
 project  string
 location string
 name     string
 target   string
 percent  int32
}

func (m *mockGCP) ShiftTraffic(_ context.Context, project, location, name, target string, percent int32) error {
 m.shiftTrafficCalled = true
 m.shiftTrafficCalls = append(m.shiftTrafficCalls, gcpCall{project: project, location: location, name: name, target: target, percent: percent})
 return m.shiftTrafficErr
}
```

Apply same pattern to `ExecuteJob`, `TriggerBackup`, `UpdateWorkerPool`.

- [ ] **Step 2: Add Project/Location to all test ApprovalRequest fixtures**

Update `newServiceReq()`, `newJobReq()`, `newWorkerPoolReq()`:

```go
func newServiceReq() domain.ApprovalRequest {
 return domain.ApprovalRequest{
  Project:       "test-project",
  Location:      "asia-northeast1",
  ResourceType:  domain.ResourceTypeService,
  // ... rest unchanged
 }
}
```

- [ ] **Step 3: Update approveService in runops.go**

Pass `req.Project` and `req.Location` to all GCP calls and propagate to nextReq/stopReq:

```go
if err := s.gcp.ShiftTraffic(ctx, req.Project, req.Location, name, rev, percent); err != nil {
```

```go
if rerr := s.gcp.ShiftTraffic(ctx, d.project, d.location, d.name, d.target, 0); rerr != nil {
```

Update `shifted` struct to include project/location, and update nextReq/stopReq construction to copy `Project` and `Location` from `req`.

- [ ] **Step 4: Update approveJob in runops.go**

```go
if err := s.gcp.TriggerBackup(ctx, req.Project, req.ResourceNames); err != nil {
```

```go
if err := s.gcp.ExecuteJob(ctx, req.Project, req.Location, req.ResourceNames, []string{"--mode=apply"}); err != nil {
```

Add `Project` and `Location` to nextReq and denyReq construction.

- [ ] **Step 5: Update approveWorkerPool in runops.go**

Same pattern as approveService — pass `req.Project`, `req.Location` to `UpdateWorkerPool` calls and propagate to nextReq/stopReq.

- [ ] **Step 6: Write test asserting project/location are passed to controller**

Add to an existing test (e.g. `TestApproveAction_Service_Success`):

```go
if len(gcp.shiftTrafficCalls) == 0 {
 t.Fatal("expected ShiftTraffic to be called")
}
call := gcp.shiftTrafficCalls[0]
if call.project != "test-project" {
 t.Errorf("expected project %q, got %q", "test-project", call.project)
}
if call.location != "asia-northeast1" {
 t.Errorf("expected location %q, got %q", "asia-northeast1", call.location)
}
```

- [ ] **Step 7: Run usecase tests**

Run: `go test ./internal/usecase/... -v`
Expected: PASS

- [ ] **Step 8: Commit**

```bash
git add internal/usecase/
git commit -m "feat: pass project/location from ApprovalRequest to GCP Controller"
```

---

### Task 5: Slack Handler — Parse project/location from button value

**Files:**

- Modify: `internal/adapter/input/slack/handler.go:23-37,104-117`
- Modify: `internal/adapter/input/slack/handler_test.go`

- [ ] **Step 1: Write failing test for project/location parsing**

Add test in `handler_test.go`:

```go
func TestHandler_ParsesProjectAndLocation(t *testing.T) {
 av := actionValue{
  Project:      "trade-non",
  Location:     "asia-northeast1",
  ResourceType: "service",
  ResourceNames: "svc",
  Action:       "canary_10",
  IssuedAt:     time.Now().Unix(),
 }
 // ... build and send request, assert req.Project == "trade-non" and req.Location == "asia-northeast1"
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapter/input/slack/... -run TestHandler_ParsesProjectAndLocation -v`
Expected: FAIL (actionValue has no Project/Location fields yet)

- [ ] **Step 3: Add Project/Location to actionValue and handler mapping**

In `handler.go`, add to `actionValue`:

```go
type actionValue struct {
 Project          string `json:"project"`
 Location         string `json:"location"`
 ResourceType     string `json:"resource_type"`
 // ... rest unchanged
}
```

In `ServeHTTP`, add to the `ApprovalRequest` construction:

```go
req := domain.ApprovalRequest{
 Project:          av.Project,
 Location:         av.Location,
 ResourceType:     domain.ResourceType(av.ResourceType),
 // ... rest unchanged
}
```

- [ ] **Step 4: Add validation — reject empty project/location**

After constructing `av`, before building `req`:

```go
if av.Project == "" || av.Location == "" {
 slog.Warn("missing project or location in button value", "project", av.Project, "location", av.Location)
 w.WriteHeader(http.StatusOK)
 return
}
```

- [ ] **Step 5: Write test for validation (empty project rejects)**

```go
func TestHandler_RejectsEmptyProject(t *testing.T) {
 av := actionValue{
  ResourceType:  "service",
  ResourceNames: "svc",
  Action:        "canary_10",
  IssuedAt:      time.Now().Unix(),
  // Project and Location intentionally empty
 }
 // ... build request, assert 200 OK returned but usecase NOT called
}
```

- [ ] **Step 6: Update all existing handler_test.go fixtures**

Add `Project: "test-project"` and `Location: "asia-northeast1"` to all `actionValue` literals in handler tests.

- [ ] **Step 7: Run all handler tests**

Run: `go test ./internal/adapter/input/slack/... -v`
Expected: PASS

- [ ] **Step 8: Commit**

```bash
git add internal/adapter/input/slack/handler.go internal/adapter/input/slack/handler_test.go
git commit -m "feat: parse and validate project/location from Slack button value"
```

---

### Task 6: Block Kit — Add project/location to marshalActionValue

**Files:**

- Modify: `internal/adapter/output/slack/blockkit.go:267-297`
- Modify: `internal/adapter/output/slack/blockkit_test.go`

- [ ] **Step 1: Write failing test**

In `blockkit_test.go`, add a test that `marshalActionValue` includes `project` and `location`:

```go
func TestMarshalActionValue_IncludesProjectAndLocation(t *testing.T) {
 req := &domain.ApprovalRequest{
  Project:       "trade-non",
  Location:      "asia-northeast1",
  ResourceType:  domain.ResourceTypeService,
  ResourceNames: "svc",
  Action:        "canary_10",
  IssuedAt:      1700000000,
 }
 val := marshalActionValue(req)
 // decompress and parse
 av, err := parseActionValue(val)
 if err != nil {
  t.Fatalf("parseActionValue failed: %v", err)
 }
 if av.Project != "trade-non" {
  t.Errorf("expected project %q, got %q", "trade-non", av.Project)
 }
 if av.Location != "asia-northeast1" {
  t.Errorf("expected location %q, got %q", "asia-northeast1", av.Location)
 }
}
```

Note: This test imports `parseActionValue` from handler.go — both are in different packages. Either use the handler's test helper or decode manually. Since `blockkit_test.go` is in `package slack` (output/slack), and `parseActionValue` is in `package slack` (input/slack), use direct gzip+base64 decode in the test.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapter/output/slack/... -run TestMarshalActionValue_IncludesProjectAndLocation -v`
Expected: FAIL

- [ ] **Step 3: Add Project/Location to progressActionValue and marshalActionValue**

```go
type progressActionValue struct {
 Project          string `json:"project"`
 Location         string `json:"location"`
 ResourceType     string `json:"resource_type"`
 // ... rest unchanged
}

func marshalActionValue(req *domain.ApprovalRequest) string {
 v := progressActionValue{
  Project:          req.Project,
  Location:         req.Location,
  ResourceType:     string(req.ResourceType),
  // ... rest unchanged
 }
```

- [ ] **Step 4: Update existing blockkit_test.go fixtures**

Add `Project` and `Location` to all `domain.ApprovalRequest` literals in blockkit tests.

- [ ] **Step 5: Run all blockkit tests**

Run: `go test ./internal/adapter/output/slack/... -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/adapter/output/slack/
git commit -m "feat: include project/location in marshalActionValue button values"
```

---

### Task 7: CLI — Add --project and --location flags

**Files:**

- Modify: `internal/adapter/input/cli/approve.go`
- Modify: `internal/adapter/input/cli/deny.go`

- [ ] **Step 1: Update approve.go**

Add `--project` and `--location` flags (required):

```go
func newApproveCmd(useCase port.RunOpsUseCase) *cobra.Command {
 var action, target, approver, project, location string
 var noSlack bool

 cmd := &cobra.Command{
  // ...
  RunE: func(cmd *cobra.Command, args []string) error {
   // ...
   req := domain.ApprovalRequest{
    Project:       project,
    Location:      location,
    ResourceType:  domain.ResourceType(resourceType),
    // ... rest unchanged
   }
```

Add required flag marking:

```go
cmd.Flags().StringVar(&project, "project", "", "GCP project ID of the target resource (required)")
cmd.Flags().StringVar(&location, "location", "", "GCP region of the target resource (required)")
_ = cmd.MarkFlagRequired("project")
_ = cmd.MarkFlagRequired("location")
```

- [ ] **Step 2: Update deny.go**

Same pattern — add `--project` and `--location` flags:

```go
func newDenyCmd(useCase port.RunOpsUseCase) *cobra.Command {
 var approver, project, location string
 var noSlack bool

 // ...
 req := domain.ApprovalRequest{
  Project:       project,
  Location:      location,
  ResourceType:  domain.ResourceType(args[0]),
  // ... rest unchanged
 }
```

```go
cmd.Flags().StringVar(&project, "project", "", "GCP project ID of the target resource (required)")
cmd.Flags().StringVar(&location, "location", "", "GCP region of the target resource (required)")
_ = cmd.MarkFlagRequired("project")
_ = cmd.MarkFlagRequired("location")
```

- [ ] **Step 3: Run CLI build check**

Run: `go build ./cmd/runops/...`
Expected: Build succeeds

- [ ] **Step 4: Commit**

```bash
git add internal/adapter/input/cli/
git commit -m "feat: add required --project and --location flags to CLI"
```

---

### Task 8: Server and CLI Entrypoints — Remove Config dependency

**Files:**

- Modify: `cmd/server/main.go`
- Modify: `cmd/runops/main.go`

- [ ] **Step 1: Update cmd/server/main.go**

Replace `gcpadapter.NewController(gcpadapter.Config{...})` with `gcpadapter.NewController()`:

```go
gcpCtrl := gcpadapter.NewController()
```

Remove `projectID` and `location` from `config` struct and `loadConfig`. Keep only `slackSigningSecret` and `port`:

```go
type config struct {
 slackSigningSecret string
 port               string
}

func loadConfig() (config, error) {
 cfg := config{
  slackSigningSecret: os.Getenv("SLACK_SIGNING_SECRET"),
  port:               os.Getenv("PORT"),
 }
 if cfg.slackSigningSecret == "" {
  return config{}, fmt.Errorf("SLACK_SIGNING_SECRET is required")
 }
 if cfg.port == "" {
  cfg.port = "8080"
 }
 slog.Info("config loaded", "port", cfg.port)
 return cfg, nil
}
```

- [ ] **Step 2: Update cmd/runops/main.go**

Replace `gcpadapter.NewController(gcpadapter.Config{...})` with `gcpadapter.NewController()`. Remove `GOOGLE_CLOUD_PROJECT` and `CLOUD_RUN_LOCATION` environment variable reads:

```go
func main() {
 gcpCtrl := gcpadapter.NewController()
 notifier := slacknotifier.NewResponseURLNotifier()
 authChecker := auth.NewEnvAuthChecker()
 svc := usecase.NewRunOpsService(gcpCtrl, notifier, authChecker, state.NewMemoryStore())

 root := cli.NewRootCmd(svc)
 if err := root.Execute(); err != nil {
  os.Exit(1)
 }
}
```

- [ ] **Step 3: Build both binaries**

Run: `go build ./cmd/...`
Expected: Build succeeds

- [ ] **Step 4: Run all tests**

Run: `go test ./... -v`
Expected: ALL PASS

- [ ] **Step 5: Commit**

```bash
git add cmd/
git commit -m "refactor: remove GCP Config from server and CLI entrypoints"
```

---

### Task 9: notify-slack.sh — Add PROJECT_ID and REGION to button values

**Files:**

- Modify: `scripts/notify-slack.sh`
- Modify: `internal/adapter/input/slack/notify_script_test.go`

- [ ] **Step 1: Write failing test**

In `notify_script_test.go`, update `TestNotifyScript_DryRun_ProducesValidJSON` to pass 7 args (add PROJECT_ID and REGION):

```go
cmd := exec.Command("bash", notifyScript(t),
 "--dry-run",
 "frontend-service",
 "db-migrate-job",
 "main",
 "abc1234567890abcdef",
 "frontend-service-00001-abc",
 "test-project",
 "asia-northeast1",
)
```

Add a new test `TestNotifyScript_ButtonValuesContainProjectAndLocation`:

```go
func TestNotifyScript_ButtonValuesContainProjectAndLocation(t *testing.T) {
 skipIfToolMissing(t, "bash", "gzip", "base64", "jq")
 cmd := exec.Command("bash", notifyScript(t),
  "--dry-run",
  "frontend-service",
  "db-migrate-job",
  "main",
  "abc1234567890abcdef",
  "frontend-service-00001-abc",
  "my-app-project",
  "us-central1",
 )
 out, err := cmd.Output()
 if err != nil {
  t.Fatalf("script failed: %v", err)
 }
 // decode each button value and check project/location
 // ... parse JSON, iterate buttons, decompress gz values, assert project=="my-app-project" and location=="us-central1"
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapter/input/slack/... -run TestNotifyScript_ButtonValuesContainProjectAndLocation -v`
Expected: FAIL (script still takes 5 args)

- [ ] **Step 3: Update notify-slack.sh**

Add `PROJECT_ID` and `REGION` as arguments 6 and 7:

```bash
if [[ $# -lt 7 ]]; then
  echo "Usage: $0 [--dry-run] SERVICE_NAMES MIGRATION_JOB_NAME BRANCH_NAME COMMIT_SHA REVISIONS PROJECT_ID REGION" >&2
  exit 1
fi

SERVICE_NAMES="$1"
MIGRATION_JOB_NAME="$2"
BRANCH_NAME="$3"
COMMIT_SHA="$4"
REVISIONS="$5"
PROJECT_ID="$6"
REGION="$7"
```

Add `--arg p "${PROJECT_ID}"` and `--arg l "${REGION}"` to all three `jq` commands that build button values, and include `project:$p, location:$l` in each JSON template:

```bash
JOB_ACTION=$(jq -n \
  --arg p   "${PROJECT_ID}" \
  --arg l   "${REGION}" \
  --arg rt  "job" \
  --arg rn  "${MIGRATION_JOB_NAME}" \
  # ... existing args ...
  '{project:$p, location:$l, resource_type:$rt, resource_names:$rn, ...}')
```

Same for `SRV_ACTION` and `DENY_ACTION`.

- [ ] **Step 4: Update all existing notify-slack tests**

Update all test invocations to pass 7 args (add project and region).

- [ ] **Step 5: Run all notify-slack tests**

Run: `go test ./internal/adapter/input/slack/... -run TestNotifyScript -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add scripts/notify-slack.sh internal/adapter/input/slack/notify_script_test.go
git commit -m "feat: add project/location to notify-slack.sh button values"
```

---

### Task 10: cloudbuild.yaml — Pass PROJECT_ID and REGION to notify-slack.sh

**Files:**

- Modify: `cloudbuild.yaml`

- [ ] **Step 1: Update notify-slack step**

Add `"${PROJECT_ID}"` and `"${_REGION}"` to the script invocation:

```yaml
      - |
        apt-get update -qq && apt-get install -yqq jq > /dev/null 2>&1
        /workspace/scripts/notify-slack.sh \
          "${_SERVICE_NAMES}" \
          "${_MIGRATION_JOB_NAME}" \
          "${BRANCH_NAME}" \
          "${COMMIT_SHA}" \
          "$$(cat /workspace/revisions.txt)" \
          "${PROJECT_ID}" \
          "${_REGION}"
```

- [ ] **Step 2: Run cloudbuild escape test**

Run: `go test ./internal/adapter/input/slack/... -run TestCloudbuild -v`
Expected: PASS (no new bash variables introduced)

- [ ] **Step 3: Commit**

```bash
git add cloudbuild.yaml
git commit -m "feat: pass PROJECT_ID and REGION to notify-slack.sh in Cloud Build"
```

---

### Task 11: Scenario Tests (runn) — Add project/location to payloads

**Files:**

- Modify: `tests/runn/approve_canary.yaml`
- Modify: `tests/runn/deny_operation.yaml`

- [ ] **Step 1: Update approve_canary.yaml**

Add `"project":"test-project","location":"asia-northeast1"` to the URL-encoded button value JSON in the request body.

- [ ] **Step 2: Update deny_operation.yaml**

Same — add `project` and `location` to the button value JSON.

- [ ] **Step 3: Run scenario tests (if server available)**

Run: `SLACK_SIGNING_SECRET=test-secret PORT=8080 go run ./cmd/server &` then `just test-runn`

- [ ] **Step 4: Commit**

```bash
git add tests/runn/
git commit -m "test: add project/location to runn scenario test payloads"
```

---

### Task 12: Documentation — Update README and slack-setup

**Files:**

- Modify: `README.md`
- Modify: `docs/slack-setup.md`

- [ ] **Step 1: Update README environment variables table**

Remove `CLOUD_RUN_LOCATION` from server env vars table. Update CLI section to show `--project` and `--location` as required flags. Update CLI usage examples.

- [ ] **Step 2: Update CLI usage examples in README**

```bash
# カナリアリリース (10%)
runops approve service your-service \
  --project=your-project --location=asia-northeast1 \
  --action=canary_10 --target=YOUR_REVISION_NAME --no-slack
```

- [ ] **Step 3: Run markdown lint**

Run: `just lint-md`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add README.md docs/
git commit -m "docs: update CLI usage and env vars for cross-project support"
```

---

### Task 13: Final Integration Test — Full pipeline verification

- [ ] **Step 1: Run all tests**

Run: `just check`
Expected: ALL PASS (fmt + lint + lint-md + test)

- [ ] **Step 2: Run go vet**

Run: `go vet ./...`
Expected: No warnings

- [ ] **Step 3: Verify init-app still works**

Run: `just init-app /tmp/test-init my-proj my-svc my-job asia-northeast1 "" my-gateway`
Verify `cloudbuild.yaml` has `"${PROJECT_ID}"` and `"${_REGION}"` in notify-slack step.

- [ ] **Step 4: Final commit (if any remaining changes)**

```bash
git add -A
git commit -m "chore: final cleanup for cross-project support"
```
