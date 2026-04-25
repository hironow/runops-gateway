package slack

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// projectRoot resolves the repository root from this file's location.
// File is at internal/adapter/input/slack/ → 4 levels up.
func projectRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "../../../../")
}

func notifyScript(t *testing.T) string {
	t.Helper()
	return filepath.Join(projectRoot(t), "scripts/notify-slack.sh")
}

func skipIfToolMissing(t *testing.T, tools ...string) {
	t.Helper()
	for _, tool := range tools {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("required tool %q not found: %v", tool, err)
		}
	}
}

func TestNotifyScript_DryRun_ProducesValidJSON(t *testing.T) {
	skipIfToolMissing(t, "bash", "gzip", "base64", "jq")

	// when — run the script in dry-run mode with known inputs
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
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("script failed: %v\nstderr: %s", err,
			func() string {
				if ee, ok := err.(*exec.ExitError); ok {
					return string(ee.Stderr)
				}
				return ""
			}())
	}

	// then — output must be valid JSON
	var payload map[string]any
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("dry-run output is not valid JSON: %v\noutput: %s", err, out)
	}

	// must have a blocks array
	blocks, ok := payload["blocks"].([]any)
	if !ok || len(blocks) == 0 {
		t.Fatal("expected non-empty blocks array")
	}
}

func TestNotifyScript_DryRun_ButtonValuesGzPrefixed(t *testing.T) {
	skipIfToolMissing(t, "bash", "gzip", "base64", "jq")

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
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("script failed: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	// collect all button values from actions blocks
	var buttonValues []string
	for _, b := range payload["blocks"].([]any) {
		block := b.(map[string]any)
		if block["type"] != "actions" {
			continue
		}
		for _, e := range block["elements"].([]any) {
			el := e.(map[string]any)
			if v, ok := el["value"].(string); ok {
				buttonValues = append(buttonValues, v)
			}
		}
	}

	if len(buttonValues) == 0 {
		t.Fatal("no button values found in payload")
	}
	for _, v := range buttonValues {
		if !strings.HasPrefix(v, "gz:") {
			t.Errorf("button value must start with 'gz:', got %q", v[:min(30, len(v))])
		}
	}
}

// TestNotifyScript_EndToEnd is the full pipeline test:
//
//	notify-slack.sh → curl POST → mock Slack server → parseActionValue
//
// This confirms that the script's output can be received and decoded by the Go
// handler without any --dry-run bypass — the same path taken in production.
func TestNotifyScript_EndToEnd_PostToMockSlack_ButtonValuesDecodable(t *testing.T) {
	skipIfToolMissing(t, "bash", "gzip", "base64", "jq", "curl")

	// given — mock Slack webhook server that captures the full POST body
	var receivedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// when — run the script for real (no --dry-run), targeting the mock server
	cmd := exec.Command("bash", notifyScript(t),
		"frontend-service,backend-service",
		"db-migrate-job",
		"main",
		"abc1234567890abcdef",
		"frontend-service-00001-abc,backend-service-00001-def",
		"test-project",
		"asia-northeast1",
	)
	cmd.Env = append(os.Environ(), "SLACK_WEBHOOK_URL="+srv.URL)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("script failed: %v\noutput: %s", err, out)
	}

	// then — mock server must have received a POST
	if len(receivedBody) == 0 {
		t.Fatal("mock Slack server received no payload")
	}

	// then — payload is valid JSON with a blocks array
	var payload map[string]any
	if err := json.Unmarshal(receivedBody, &payload); err != nil {
		t.Fatalf("payload is not valid JSON: %v\nbody: %s", err, receivedBody)
	}
	blocks, ok := payload["blocks"].([]any)
	if !ok || len(blocks) == 0 {
		t.Fatal("expected non-empty blocks array in payload")
	}

	// then — every button value is gz: prefixed AND decodable by parseActionValue
	var checked int
	for _, b := range blocks {
		block, ok := b.(map[string]any)
		if !ok || block["type"] != "actions" {
			continue
		}
		elements, ok := block["elements"].([]any)
		if !ok {
			continue
		}
		for _, e := range elements {
			el, ok := e.(map[string]any)
			if !ok {
				continue
			}
			val, ok := el["value"].(string)
			if !ok {
				continue
			}
			if !strings.HasPrefix(val, "gz:") {
				t.Errorf("button value must start with 'gz:', got %q", val[:min(30, len(val))])
			}
			av, err := parseActionValue(val)
			if err != nil {
				t.Errorf("parseActionValue failed: %v (value prefix: %q)", err, val[:min(30, len(val))])
				continue
			}
			if av.ResourceNames == "" && av.ResourceName == "" {
				t.Error("decoded action value has no resource name")
			}
			checked++
		}
	}
	if checked == 0 {
		t.Error("no button values found to validate")
	}
}

func TestNotifyScript_ButtonValuesContainProjectAndLocation(t *testing.T) {
	skipIfToolMissing(t, "bash", "gzip", "base64", "jq")

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
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("script failed: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	// collect and decode all button values
	var checked int
	for _, b := range payload["blocks"].([]any) {
		block := b.(map[string]any)
		if block["type"] != "actions" {
			continue
		}
		for _, e := range block["elements"].([]any) {
			el := e.(map[string]any)
			val, ok := el["value"].(string)
			if !ok {
				continue
			}
			av, err := parseActionValue(val)
			if err != nil {
				t.Errorf("parseActionValue failed: %v", err)
				continue
			}
			if av.Project != "test-project" {
				t.Errorf("button %q: Project = %q, want %q", el["action_id"], av.Project, "test-project")
			}
			if av.Location != "asia-northeast1" {
				t.Errorf("button %q: Location = %q, want %q", el["action_id"], av.Location, "asia-northeast1")
			}
			checked++
		}
	}
	if checked == 0 {
		t.Fatal("no button values found to validate")
	}
}

// TestNotifyScript_WorkerPool_* verify that when WORKER_POOL_NAMES (arg 8) and
// WORKER_POOL_REVISIONS (arg 9) are supplied, an extra "3. Worker Pool Canary"
// button is emitted with action_id=approve_worker_pool and resource_type=worker-pool.
// Without those args, the payload retains its original 3-button shape.

func TestNotifyScript_NoWorkerPool_HasThreeButtons(t *testing.T) {
	skipIfToolMissing(t, "bash", "gzip", "base64", "jq")

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
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("script failed: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	got := actionIDs(t, payload)
	want := []string{"approve_job", "approve_service", "deny"}
	if !sliceEqual(got, want) {
		t.Errorf("action_ids = %v, want %v", got, want)
	}
}

func TestNotifyScript_EmptyMigrationJob_SuppressesMigrationButton(t *testing.T) {
	// Regression: apps without a Cloud SQL backed migration job (e.g. static
	// sites like nn-makers) leave _MIGRATION_JOB_NAME empty in cloudbuild.yaml.
	// Pressing button 1 in that case fires a Cloud SQL backup against an
	// empty/non-existent instance and 403/404s. The button must not appear.
	skipIfToolMissing(t, "bash", "gzip", "base64", "jq")

	cmd := exec.Command("bash", notifyScript(t),
		"--dry-run",
		"nn-makers",
		"", // ← MIGRATION_JOB_NAME empty
		"main",
		"abc1234567890abcdef",
		"nn-makers-00001-abc",
		"test-project",
		"asia-northeast1",
	)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("script failed: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	got := actionIDs(t, payload)
	want := []string{"approve_service", "deny"}
	if !sliceEqual(got, want) {
		t.Errorf("action_ids = %v, want %v (button 1 must be suppressed when MIGRATION_JOB_NAME empty)", got, want)
	}
}

func TestNotifyScript_WithWorkerPool_HasFourButtons(t *testing.T) {
	skipIfToolMissing(t, "bash", "gzip", "base64", "jq")

	cmd := exec.Command("bash", notifyScript(t),
		"--dry-run",
		"frontend-service",
		"db-migrate-job",
		"main",
		"abc1234567890abcdef",
		"frontend-service-00001-abc",
		"test-project",
		"asia-northeast1",
		"async-worker,batch-worker",
		"async-worker-00002-xxx,batch-worker-00002-yyy",
	)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("script failed: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	got := actionIDs(t, payload)
	want := []string{"approve_job", "approve_service", "approve_worker_pool", "deny"}
	if !sliceEqual(got, want) {
		t.Errorf("action_ids = %v, want %v", got, want)
	}
}

func TestNotifyScript_WithWorkerPool_ButtonEncodesResourceType(t *testing.T) {
	skipIfToolMissing(t, "bash", "gzip", "base64", "jq")

	cmd := exec.Command("bash", notifyScript(t),
		"--dry-run",
		"frontend-service",
		"db-migrate-job",
		"main",
		"abc1234567890abcdef",
		"frontend-service-00001-abc",
		"test-project",
		"asia-northeast1",
		"async-worker,batch-worker",
		"async-worker-00002-xxx,batch-worker-00002-yyy",
	)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("script failed: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	wpVal := buttonValue(t, payload, "approve_worker_pool")
	if wpVal == "" {
		t.Fatal("approve_worker_pool button not found")
	}
	av, err := parseActionValue(wpVal)
	if err != nil {
		t.Fatalf("parseActionValue failed: %v", err)
	}
	if string(av.ResourceType) != "worker-pool" {
		t.Errorf("ResourceType = %q, want %q", av.ResourceType, "worker-pool")
	}
	if av.ResourceNames != "async-worker,batch-worker" {
		t.Errorf("ResourceNames = %q, want %q", av.ResourceNames, "async-worker,batch-worker")
	}
	if av.Targets != "async-worker-00002-xxx,batch-worker-00002-yyy" {
		t.Errorf("Targets = %q, want %q", av.Targets, "async-worker-00002-xxx,batch-worker-00002-yyy")
	}
	if av.Action != "canary_10" {
		t.Errorf("Action = %q, want %q", av.Action, "canary_10")
	}
	if !av.MigrationDone {
		t.Error("MigrationDone = false, want true (worker pool promotion is independent of DB migration)")
	}
}

// actionIDs extracts the action_id of every button in the "actions" block.
func actionIDs(t *testing.T, payload map[string]any) []string {
	t.Helper()
	var ids []string
	for _, b := range payload["blocks"].([]any) {
		block := b.(map[string]any)
		if block["type"] != "actions" {
			continue
		}
		for _, e := range block["elements"].([]any) {
			el := e.(map[string]any)
			if id, ok := el["action_id"].(string); ok {
				ids = append(ids, id)
			}
		}
	}
	return ids
}

// buttonValue returns the "value" of the button with the given action_id,
// or "" if not found.
func buttonValue(t *testing.T, payload map[string]any, actionID string) string {
	t.Helper()
	for _, b := range payload["blocks"].([]any) {
		block := b.(map[string]any)
		if block["type"] != "actions" {
			continue
		}
		for _, e := range block["elements"].([]any) {
			el := e.(map[string]any)
			if el["action_id"] == actionID {
				if v, ok := el["value"].(string); ok {
					return v
				}
			}
		}
	}
	return ""
}

func sliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestNotifyScript_CompressGz_CompatibleWithParseActionValue(t *testing.T) {
	skipIfToolMissing(t, "bash", "gzip", "base64")

	// given — compress a known JSON string using bash's compress_gz (same logic as cloudbuild.yaml)
	input := `{"resource_type":"service","resource_names":"frontend-service","targets":"frontend-service-00001-abc","action":"canary_10","issued_at":1700000000,"migration_done":false}`

	cmd := exec.Command("bash", "-c",
		`printf '%s' "$1" | gzip -c | base64 -w 0 | tr '+/' '-_' | tr -d '='`,
		"--", input,
	)
	raw, err := cmd.Output()
	if err != nil {
		t.Fatalf("bash compress failed: %v", err)
	}

	compressed := "gz:" + strings.TrimSpace(string(raw))

	// when — decode with Go's parseActionValue
	av, err := parseActionValue(compressed)

	// then — round-trip must preserve all fields
	if err != nil {
		t.Fatalf("parseActionValue failed: %v", err)
	}
	if av.ResourceNames != "frontend-service" {
		t.Errorf("ResourceNames: got %q, want %q", av.ResourceNames, "frontend-service")
	}
	if av.Targets != "frontend-service-00001-abc" {
		t.Errorf("Targets: got %q, want %q", av.Targets, "frontend-service-00001-abc")
	}
	if av.Action != "canary_10" {
		t.Errorf("Action: got %q, want %q", av.Action, "canary_10")
	}
}

func TestNotifyScript_BuildInfoEmbeddedInAllButtons(t *testing.T) {
	// All three buttons (job, service, deny) must carry the same build_info
	// so the Slack handler can show "Build: main @ <sha>" in every subsequent
	// progress / rebuild message — even after the operator clicks through.
	skipIfToolMissing(t, "bash", "gzip", "base64", "jq")

	cmd := exec.Command("bash", notifyScript(t),
		"--dry-run",
		"frontend-service",
		"db-migrate-job",
		"main",
		"d948375abcdef12345",
		"frontend-service-00001-abc",
		"test-project",
		"asia-northeast1",
	)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("script failed: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	want := "main @ d948375"
	for _, id := range []string{"approve_job", "approve_service", "deny"} {
		v := buttonValue(t, payload, id)
		if v == "" {
			t.Errorf("button %s missing", id)
			continue
		}
		decoded := decodeGz(t, v)
		bi, _ := decoded["build_info"].(string)
		if bi != want {
			t.Errorf("button %s build_info = %q, want %q", id, bi, want)
		}
	}
}

// decodeGz decompresses a "gz:<base64url>" button value back to a JSON map.
func decodeGz(t *testing.T, value string) map[string]any {
	t.Helper()
	if !strings.HasPrefix(value, "gz:") {
		t.Fatalf("expected gz: prefix, got %q", value[:min(len(value), 20)])
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, "gz:"))
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("gzip new reader: %v", err)
	}
	plain, err := io.ReadAll(gz)
	if err != nil {
		t.Fatalf("gzip read: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(plain, &out); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	return out
}
