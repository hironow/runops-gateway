package unit_test

// Tests for scripts/check-drift-gate-parity.sh (ADR 0043). The script asserts
// that the TF_VAR set is consistent across three layers (cd.yaml infra job,
// cd.yaml drift-gate job with:, and the composite action). These tests run the
// real script against the real files (must pass) and against fixtures with a
// deliberately dropped variable (must fail), proving the guard catches drift
// between the layers — not just that it is green today.

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd() // tests/unit
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Clean(filepath.Join(wd, "..", ".."))
}

func parityScript(t *testing.T) string {
	t.Helper()
	return filepath.Join(repoRoot(t), "scripts", "check-drift-gate-parity.sh")
}

// runParity runs the script with explicit cd.yaml / action.yaml paths and
// returns its exit code plus combined output.
func runParity(t *testing.T, cdPath, actionPath string) (int, string) {
	t.Helper()
	cmd := exec.Command("bash", parityScript(t), cdPath, actionPath)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return 0, string(out)
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), string(out)
	}
	t.Fatalf("running parity script: %v\n%s", err, out)
	return -1, ""
}

// writeFixtureDroppingFirst copies src to a temp file, removing the first line
// for which drop returns true. It fails the test if no line matched.
func writeFixtureDroppingFirst(t *testing.T, src string, drop func(string) bool) string {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read %s: %v", src, err)
	}
	lines := strings.Split(string(data), "\n")
	dropped := false
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if !dropped && drop(line) {
			dropped = true
			continue
		}
		kept = append(kept, line)
	}
	if !dropped {
		t.Fatalf("fixture: no line matched the drop predicate in %s", src)
	}
	dst := filepath.Join(t.TempDir(), filepath.Base(src))
	if err := os.WriteFile(dst, []byte(strings.Join(kept, "\n")), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return dst
}

func realCD(t *testing.T) string {
	t.Helper()
	return filepath.Join(repoRoot(t), ".github", "workflows", "cd.yaml")
}

func realAction(t *testing.T) string {
	t.Helper()
	return filepath.Join(repoRoot(t), ".github", "actions", "tofu-drift-gate", "action.yaml")
}

func TestParity_PassesOnRealFiles(t *testing.T) {
	// given: the committed cd.yaml + action.yaml
	// when
	code, out := runParity(t, realCD(t), realAction(t))
	// then
	if code != 0 {
		t.Fatalf("expected exit 0 on real files, got %d:\n%s", code, out)
	}
}

func TestParity_FailsWhenActionDropsTFVar(t *testing.T) {
	// given: action.yaml with one TF_VAR env line removed
	brokenAction := writeFixtureDroppingFirst(t, realAction(t), func(line string) bool {
		return strings.Contains(line, "TF_VAR_dlq_alert_email:")
	})
	// when
	code, out := runParity(t, realCD(t), brokenAction)
	// then
	if code == 0 {
		t.Fatalf("expected non-zero exit when action drops a TF_VAR, got 0:\n%s", out)
	}
}

func TestParity_FailsWhenDriftGateDropsInput(t *testing.T) {
	// given: cd.yaml with the drift-gate `with:` dlq_alert_email input removed
	// (the infra TF_VAR_dlq_alert_email line carries the TF_VAR_ prefix, so it
	// is left intact — only the drift-gate input line is dropped).
	brokenCD := writeFixtureDroppingFirst(t, realCD(t), func(line string) bool {
		trimmed := strings.TrimSpace(line)
		return strings.HasPrefix(trimmed, "dlq_alert_email:")
	})
	// when
	code, out := runParity(t, brokenCD, realAction(t))
	// then
	if code == 0 {
		t.Fatalf("expected non-zero exit when drift-gate drops an input, got 0:\n%s", out)
	}
}
