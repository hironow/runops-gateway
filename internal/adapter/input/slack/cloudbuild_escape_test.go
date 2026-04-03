package slack

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestCloudbuild_BashVarsEscaped verifies that bash-local variables in
// cloudbuild.yaml inline scripts use $$ (Cloud Build escape) instead of $.
//
// Cloud Build interprets every $VAR and ${VAR} as a substitution variable.
// Bash-local variables must be written as $$VAR / $${VAR} / $$(cmd) so that
// Cloud Build passes a literal $ to the shell.
//
// Allowed single-$ patterns (Cloud Build built-ins and user substitutions):
//
//	${PROJECT_ID}, ${COMMIT_SHA}, ${BRANCH_NAME}, ${_IMAGE}, ${_SERVICE_NAMES}, etc.
func TestCloudbuild_BashVarsEscaped(t *testing.T) {
	content, err := os.ReadFile(filepath.Join(projectRoot(t), "cloudbuild.yaml"))
	if err != nil {
		t.Fatalf("failed to read cloudbuild.yaml: %v", err)
	}

	// Cloud Build built-in and user-defined substitution patterns.
	// These are the ONLY variables allowed to use single $.
	allowedSubstitutions := map[string]bool{
		"PROJECT_ID":          true,
		"COMMIT_SHA":          true,
		"BRANCH_NAME":         true,
		"BUILD_ID":            true,
		"REPO_NAME":           true,
		"REPO_FULL_NAME":      true,
		"REVISION_ID":         true,
		"SHORT_SHA":           true,
		"TRIGGER_NAME":        true,
		"TRIGGER_BUILD_CONFIG_PATH": true,
		"SERVICE_ACCOUNT_EMAIL":     true,
		"LOCATION":            true,
	}

	// User-defined substitutions start with _.
	// Match ${VAR} and $VAR patterns (single $, not preceded by another $).
	// (?:^|[^$]) ensures we don't match the second $ in $$.
	re := regexp.MustCompile(`(?:^|[^$])\$\{?([A-Za-z_][A-Za-z0-9_]*)`)

	// Also match $( for command substitution — must be $$(
	reCmdSub := regexp.MustCompile(`(?:^|[^$])\$\(`)

	lines := strings.Split(string(content), "\n")
	inBashBlock := false

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Detect start/end of bash inline script blocks (after "- |")
		if trimmed == "- |" {
			inBashBlock = true
			continue
		}
		// End of bash block: line that is not indented enough or is a YAML key
		if inBashBlock && !strings.HasPrefix(line, "        ") && trimmed != "" {
			inBashBlock = false
		}
		if !inBashBlock {
			continue
		}

		// Check for unescaped command substitutions: $( without preceding $
		for _, loc := range reCmdSub.FindAllStringIndex(line, -1) {
			col := loc[0]
			t.Errorf("cloudbuild.yaml:%d:%d: unescaped command substitution $( — use $$(\n  %s", i+1, col+1, trimmed)
		}

		// Check for unescaped variable references
		for _, match := range re.FindAllStringSubmatch(line, -1) {
			varName := match[1]
			// Allow Cloud Build built-ins
			if allowedSubstitutions[varName] {
				continue
			}
			// Allow user-defined substitutions (start with _)
			if strings.HasPrefix(varName, "_") {
				continue
			}
			t.Errorf("cloudbuild.yaml:%d: unescaped bash variable $%s — use $$%s\n  %s", i+1, varName, varName, trimmed)
		}
	}
}
