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
	content := runInitApp(t, target, "test-project", "my-api", "my-migrate", "asia-northeast1", "", "")

	// then
	if !strings.Contains(content, "_SERVICE_NAMES: my-api") {
		t.Error("expected _SERVICE_NAMES to be substituted")
	}
	if !strings.Contains(content, "_MIGRATION_JOB_NAME: my-migrate") {
		t.Error("expected _MIGRATION_JOB_NAME to be substituted")
	}
}

func TestInitApp_SubstitutesAppProject(t *testing.T) {
	skipIfToolMissing(t, "bash")

	// given
	target := t.TempDir()

	// when
	content := runInitApp(t, target, "my-gcp-project", "svc", "job")

	// then — ${PROJECT_ID} must be replaced with the app project
	if strings.Contains(content, "${PROJECT_ID}") {
		t.Error("expected all ${PROJECT_ID} to be replaced with app project")
	}
	if !strings.Contains(content, "my-gcp-project") {
		t.Error("expected app project to appear in generated cloudbuild.yaml")
	}
}

func TestInitApp_SubstitutesGatewayProject(t *testing.T) {
	skipIfToolMissing(t, "bash")

	// given
	target := t.TempDir()

	// when
	content := runInitApp(t, target, "app-proj", "svc", "job", "asia-northeast1", "", "my-gateway")

	// then
	if !strings.Contains(content, "_GATEWAY_PROJECT: my-gateway") {
		t.Error("expected _GATEWAY_PROJECT to be substituted with my-gateway")
	}
}

func TestInitApp_DefaultGatewayProject_UsesAppProject(t *testing.T) {
	skipIfToolMissing(t, "bash")

	// given
	target := t.TempDir()

	// when
	content := runInitApp(t, target, "my-app-proj", "svc", "job", "asia-northeast1", "", "")

	// then — ${PROJECT_ID} is replaced with app project, so gateway defaults to app project
	if !strings.Contains(content, "_GATEWAY_PROJECT: my-app-proj") {
		t.Errorf("expected _GATEWAY_PROJECT to default to app project, got:\n%s",
			extractLine(content, "_GATEWAY_PROJECT:"))
	}
}

func TestInitApp_ArtifactRepoDefaultsToFirstService(t *testing.T) {
	skipIfToolMissing(t, "bash")

	// given
	target := t.TempDir()

	// when — multi-service with no explicit artifact repo
	content := runInitApp(t, target, "proj", "frontend,backend", "job", "asia-northeast1", "", "")

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
	content := runInitApp(t, target, "proj", "svc", "job", "asia-northeast1", "custom-repo", "")

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
	runInitApp(t, target, "proj", "svc", "job")

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
	cmd := exec.Command("bash", initAppScript(t), "/nonexistent/path", "proj", "svc", "job")
	out, err := cmd.CombinedOutput()

	// then
	if err == nil {
		t.Fatal("expected init-app.sh to fail for non-existent target")
	}
	if !strings.Contains(string(out), "does not exist") {
		t.Errorf("expected error message about non-existent directory, got: %s", out)
	}
}

func TestInitApp_NoProjectIDRemains(t *testing.T) {
	skipIfToolMissing(t, "bash")

	// given
	target := t.TempDir()

	// when
	content := runInitApp(t, target, "real-project", "svc", "job", "asia-northeast1", "", "gw-project")

	// then — no ${PROJECT_ID} should remain anywhere
	if strings.Contains(content, "${PROJECT_ID}") {
		t.Error("${PROJECT_ID} should not remain in generated cloudbuild.yaml")
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
