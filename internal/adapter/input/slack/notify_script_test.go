package slack

import (
	"encoding/json"
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
