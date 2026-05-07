//go:build integration

package integration

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hironow/runops-gateway/internal/adapter/input/cli"
	"github.com/hironow/runops-gateway/internal/adapter/output/state"
)

// TestProjectLifecycle_E2E exercises the runops project subcommand against
// a real SQLite file (no mocks), covering the operator's day-1 workflow:
// add → list → show → archive → list --status archived.
//
// Driven via cli.NewRootCmd so the cobra parser, the env-driven factory,
// and the SQLite adapter are all in the test path.
func TestProjectLifecycle_E2E(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state.db")

	// Construct registry through the env-driven factory (production path).
	getenv := func(key string) string {
		switch key {
		case "RUNOPS_PROJECT_REGISTRY":
			return "sqlite"
		case "RUNOPS_STATE_DB_PATH":
			return dbPath
		}
		return ""
	}
	registry, err := state.NewProjectRegistryFromEnv(context.Background(), getenv)
	if err != nil {
		t.Fatalf("registry init: %v", err)
	}

	// Helper: run a CLI invocation against a fresh root cmd.
	run := func(args ...string) (string, error) {
		out := &bytes.Buffer{}
		errBuf := &bytes.Buffer{}
		root := cli.NewRootCmd(nil, registry)
		root.SetOut(out)
		root.SetErr(errBuf)
		root.SetArgs(args)
		err := root.Execute()
		return out.String() + errBuf.String(), err
	}

	// Add foo.
	if _, err := run("project", "add", "foo",
		"--org", "hironow", "--repo", "demo",
		"--workspace", "/home/coder/projects/foo",
		"--slack-channel", "#runops",
		"--installation-id", "55555"); err != nil {
		t.Fatalf("add foo: %v", err)
	}

	// Add bar.
	if _, err := run("project", "add", "bar",
		"--org", "hironow", "--repo", "another",
		"--workspace", "/home/coder/projects/bar"); err != nil {
		t.Fatalf("add bar: %v", err)
	}

	// List active: both visible.
	out, err := run("project", "list", "--status", "active")
	if err != nil {
		t.Fatalf("list active: %v", err)
	}
	for _, want := range []string{"foo", "bar", "active"} {
		if !strings.Contains(out, want) {
			t.Errorf("list active output missing %q in %q", want, out)
		}
	}

	// Show foo: roundtripped fields.
	out, err = run("project", "show", "foo")
	if err != nil {
		t.Fatalf("show foo: %v", err)
	}
	for _, want := range []string{"foo", "hironow", "demo", "/home/coder/projects/foo", "#runops", "55555", "active"} {
		if !strings.Contains(out, want) {
			t.Errorf("show foo missing %q in %q", want, out)
		}
	}

	// Archive foo.
	if _, err := run("project", "archive", "foo"); err != nil {
		t.Fatalf("archive foo: %v", err)
	}

	// archive idempotent: second call must not error.
	if _, err := run("project", "archive", "foo"); err != nil {
		t.Fatalf("second archive (idempotent): %v", err)
	}

	// List archived: only foo.
	out, err = run("project", "list", "--status", "archived")
	if err != nil {
		t.Fatalf("list archived: %v", err)
	}
	if !strings.Contains(out, "foo") {
		t.Errorf("list archived missing foo: %q", out)
	}
	if strings.Contains(out, "bar") {
		t.Errorf("list archived should NOT include bar: %q", out)
	}

	// List active (post-archive): only bar.
	out, err = run("project", "list", "--status", "active")
	if err != nil {
		t.Fatalf("list active post-archive: %v", err)
	}
	if !strings.Contains(out, "bar") {
		t.Errorf("post-archive active missing bar: %q", out)
	}
}

func TestProjectLifecycle_RegistryFactoryFailClosed(t *testing.T) {
	// Empty env → fail-closed
	_, err := state.NewProjectRegistryFromEnv(context.Background(), func(string) string { return "" })
	if err == nil {
		t.Fatalf("empty env should fail-closed")
	}
	if !strings.Contains(err.Error(), "RUNOPS_PROJECT_REGISTRY") {
		t.Errorf("error should reference env var, got %v", err)
	}

	// Firestore reserved for #0011
	_, err = state.NewProjectRegistryFromEnv(context.Background(), func(k string) string {
		if k == "RUNOPS_PROJECT_REGISTRY" {
			return "firestore"
		}
		return ""
	})
	if err == nil || !strings.Contains(err.Error(), "0011") {
		t.Errorf("firestore should fail with #0011 reference, got %v", err)
	}
}
