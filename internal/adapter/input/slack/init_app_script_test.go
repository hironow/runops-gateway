package slack

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func initAppScript(t *testing.T) string {
	t.Helper()
	return filepath.Join(projectRoot(t), "scripts/init-app.sh")
}

// runInitApp executes init-app.sh and returns the generated cloudbuild.yaml content.
func runInitApp(t *testing.T, target string, args ...string) string {
	t.Helper()
	allArgs := append([]string{initAppScript(t), target}, args...)
	cmd := exec.Command("bash", allArgs...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("init-app.sh failed: %v\noutput: %s", err, out)
	}
	content, err := os.ReadFile(filepath.Join(target, "cloudbuild.yaml"))
	if err != nil {
		t.Fatalf("failed to read generated cloudbuild.yaml: %v", err)
	}
	return string(content)
}

func TestInitApp_SubstitutesServiceAndJob(t *testing.T) {
	skipIfToolMissing(t, "bash")

	// given
	target := t.TempDir()

	// when
	content := runInitApp(t, target, "my-api", "my-migrate", "asia-northeast1", "", "")

	// then
	if !strings.Contains(content, "_SERVICE_NAMES: my-api") {
		t.Error("expected _SERVICE_NAMES to be substituted")
	}
	if !strings.Contains(content, "_MIGRATION_JOB_NAME: my-migrate") {
		t.Error("expected _MIGRATION_JOB_NAME to be substituted")
	}
}

func TestInitApp_SubstitutesGatewayProject(t *testing.T) {
	skipIfToolMissing(t, "bash")

	// given
	target := t.TempDir()

	// when
	content := runInitApp(t, target, "svc", "job", "asia-northeast1", "", "my-gateway")

	// then
	if !strings.Contains(content, "_GATEWAY_PROJECT: my-gateway") {
		t.Error("expected _GATEWAY_PROJECT to be substituted with my-gateway")
	}
	// _GATEWAY_PROJECT line must NOT contain ${PROJECT_ID}
	for _, line := range strings.Split(content, "\n") {
		if strings.Contains(line, "_GATEWAY_PROJECT:") && strings.Contains(line, "${PROJECT_ID}") {
			t.Errorf("_GATEWAY_PROJECT line should not contain ${PROJECT_ID}, got: %s", strings.TrimSpace(line))
		}
	}
}

func TestInitApp_DefaultGatewayProject_PreservesProjectIDVar(t *testing.T) {
	skipIfToolMissing(t, "bash")

	// given
	target := t.TempDir()

	// when
	content := runInitApp(t, target, "svc", "job", "asia-northeast1", "", "")

	// then
	if !strings.Contains(content, "_GATEWAY_PROJECT: ${PROJECT_ID}") {
		t.Error("expected _GATEWAY_PROJECT to remain ${PROJECT_ID} when gateway_project is empty")
	}
}

func TestInitApp_ArtifactRepoDefaultsToFirstService(t *testing.T) {
	skipIfToolMissing(t, "bash")

	// given
	target := t.TempDir()

	// when — multi-service with no explicit artifact repo
	content := runInitApp(t, target, "frontend,backend", "job", "asia-northeast1", "", "")

	// then — artifact repo should be first service name
	if !strings.Contains(content, "frontend/frontend") {
		t.Errorf("expected artifact repo to default to first service name, got:\n%s",
			extractLine(content, "_IMAGE:"))
	}
}

func TestInitApp_ExplicitArtifactRepo(t *testing.T) {
	skipIfToolMissing(t, "bash")

	// given
	target := t.TempDir()

	// when
	content := runInitApp(t, target, "svc", "job", "asia-northeast1", "custom-repo", "")

	// then
	if !strings.Contains(content, "custom-repo/custom-repo") {
		t.Errorf("expected artifact repo to be custom-repo, got:\n%s",
			extractLine(content, "_IMAGE:"))
	}
}

func TestInitApp_CopiesNotifySlackScript(t *testing.T) {
	skipIfToolMissing(t, "bash")

	// given
	target := t.TempDir()

	// when
	runInitApp(t, target, "svc", "job")

	// then
	info, err := os.Stat(filepath.Join(target, "scripts/notify-slack.sh"))
	if err != nil {
		t.Fatalf("notify-slack.sh not copied: %v", err)
	}
	if info.Mode()&0o111 == 0 {
		t.Error("notify-slack.sh should be executable")
	}
}

func TestInitApp_FailsOnMissingTarget(t *testing.T) {
	skipIfToolMissing(t, "bash")

	// when
	cmd := exec.Command("bash", initAppScript(t), "/nonexistent/path", "svc", "job")
	out, err := cmd.CombinedOutput()

	// then
	if err == nil {
		t.Fatal("expected init-app.sh to fail for non-existent target")
	}
	if !strings.Contains(string(out), "does not exist") {
		t.Errorf("expected error message about non-existent directory, got: %s", out)
	}
}

// extractLine returns the first line containing substr, for error messages.
func extractLine(content, substr string) string {
	for _, line := range strings.Split(content, "\n") {
		if strings.Contains(line, substr) {
			return strings.TrimSpace(line)
		}
	}
	return "(not found)"
}
