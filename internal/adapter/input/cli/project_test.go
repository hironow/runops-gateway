package cli_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/hironow/runops-gateway/internal/adapter/input/cli"
	"github.com/hironow/runops-gateway/internal/core/domain"
	"github.com/hironow/runops-gateway/internal/core/port"
)

// fakeProjectRegistry is an in-memory port.ProjectRegistry for CLI tests.
type fakeProjectRegistry struct {
	mu       sync.Mutex
	projects map[string]domain.Project
	addErr   error
}

func newFakeRegistry() *fakeProjectRegistry {
	return &fakeProjectRegistry{projects: map[string]domain.Project{}}
}

func (f *fakeProjectRegistry) Add(_ context.Context, p domain.Project) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.addErr != nil {
		return f.addErr
	}
	if err := domain.ValidateProjectID(p.ID); err != nil {
		return err
	}
	if _, ok := f.projects[p.ID]; ok {
		return domain.ErrProjectAlreadyExists
	}
	if p.Status == "" {
		p.Status = domain.ProjectStatusActive
	}
	f.projects[p.ID] = p
	return nil
}

func (f *fakeProjectRegistry) Get(_ context.Context, id string) (domain.Project, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.projects[id]
	if !ok {
		return domain.Project{}, domain.ErrProjectNotFound
	}
	return p, nil
}

func (f *fakeProjectRegistry) List(_ context.Context, filter port.ProjectListFilter) ([]domain.Project, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]domain.Project, 0, len(f.projects))
	for _, p := range f.projects {
		if filter.Status != "" && p.Status != filter.Status {
			continue
		}
		out = append(out, p)
	}
	return out, nil
}

func (f *fakeProjectRegistry) Archive(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.projects[id]
	if !ok {
		return domain.ErrProjectNotFound
	}
	p.Status = domain.ProjectStatusArchived
	f.projects[id] = p
	return nil
}

func newProjectRoot(t *testing.T, reg port.ProjectRegistry) (*bytes.Buffer, *bytes.Buffer, func(args ...string) error) {
	t.Helper()
	root := cli.NewRootCmd(&mockUseCase{}, reg)
	out := &bytes.Buffer{}
	errBuf := &bytes.Buffer{}
	root.SetOut(out)
	root.SetErr(errBuf)
	return out, errBuf, func(args ...string) error {
		root.SetArgs(args)
		return root.Execute()
	}
}

func TestProjectCmd_Add_PersistsRow(t *testing.T) {
	reg := newFakeRegistry()
	out, _, run := newProjectRoot(t, reg)

	err := run("project", "add", "foo",
		"--org", "hironow", "--repo", "demo",
		"--workspace", "/home/coder/projects/foo")
	if err != nil {
		t.Fatalf("project add: %v", err)
	}
	if _, ok := reg.projects["foo"]; !ok {
		t.Errorf("project foo should be persisted")
	}
	if !strings.Contains(out.String(), "foo") {
		t.Errorf("output should mention foo, got %q", out.String())
	}
}

func TestProjectCmd_Add_RejectsInvalidID(t *testing.T) {
	reg := newFakeRegistry()
	_, _, run := newProjectRoot(t, reg)

	err := run("project", "add", "bad id",
		"--org", "hironow", "--repo", "demo",
		"--workspace", "/path")
	if err == nil {
		t.Fatalf("expected error for invalid id")
	}
	if !errors.Is(err, domain.ErrInvalidProjectID) && !strings.Contains(err.Error(), "invalid project id") {
		t.Errorf("want ErrInvalidProjectID, got %v", err)
	}
}

func TestProjectCmd_List_FormatsRows(t *testing.T) {
	reg := newFakeRegistry()
	_, _, run := newProjectRoot(t, reg)

	if err := run("project", "add", "foo", "--org", "hironow", "--repo", "r1", "--workspace", "/w/foo"); err != nil {
		t.Fatalf("seed foo: %v", err)
	}
	if err := run("project", "add", "bar", "--org", "hironow", "--repo", "r2", "--workspace", "/w/bar"); err != nil {
		t.Fatalf("seed bar: %v", err)
	}

	out, _, run := newProjectRoot(t, reg) // fresh buffers for list
	if err := run("project", "list"); err != nil {
		t.Fatalf("project list: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "foo") || !strings.Contains(got, "bar") {
		t.Errorf("list output missing rows: %q", got)
	}
}

func TestProjectCmd_Show_ProjectFields(t *testing.T) {
	reg := newFakeRegistry()
	_, _, run := newProjectRoot(t, reg)

	if err := run("project", "add", "foo", "--org", "hironow", "--repo", "demo", "--workspace", "/w/foo",
		"--slack-channel", "#runops", "--installation-id", "9999"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	out, _, run := newProjectRoot(t, reg)
	if err := run("project", "show", "foo"); err != nil {
		t.Fatalf("show: %v", err)
	}
	got := out.String()
	for _, want := range []string{"foo", "hironow", "demo", "/w/foo", "#runops", "9999", "active"} {
		if !strings.Contains(got, want) {
			t.Errorf("show output missing %q in %q", want, got)
		}
	}
}

func TestProjectCmd_Archive_ChangesStatus(t *testing.T) {
	reg := newFakeRegistry()
	_, _, run := newProjectRoot(t, reg)
	if err := run("project", "add", "foo", "--org", "h", "--repo", "r", "--workspace", "/w"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := run("project", "archive", "foo"); err != nil {
		t.Fatalf("archive: %v", err)
	}
	if reg.projects["foo"].Status != domain.ProjectStatusArchived {
		t.Errorf("after archive: status=%s, want archived", reg.projects["foo"].Status)
	}
}

func TestProjectCmd_NotAddedWhenRegistryNil(t *testing.T) {
	root := cli.NewRootCmd(&mockUseCase{}, nil)
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"project", "list"})
	err := root.Execute()
	if err == nil {
		t.Fatalf("project subcommand should not be available when registry is nil")
	}
}
