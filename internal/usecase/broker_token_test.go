package usecase_test

import (
	"context"
	"errors"
	"testing"

	"github.com/hironow/runops-gateway/internal/core/domain"
	"github.com/hironow/runops-gateway/internal/core/port"
	"github.com/hironow/runops-gateway/internal/usecase"
)

// fakeProjectRegistry is a test double for port.ProjectRegistry. The
// broker test never exercises Add/List/Archive — only Get — so the
// other methods just satisfy the interface.
type fakeProjectRegistry struct {
	projects map[string]domain.Project
}

func (f *fakeProjectRegistry) Add(_ context.Context, _ domain.Project) error {
	return errors.New("fakeProjectRegistry.Add not used by broker tests")
}

func (f *fakeProjectRegistry) List(_ context.Context, _ port.ProjectListFilter) ([]domain.Project, error) {
	return nil, errors.New("fakeProjectRegistry.List not used by broker tests")
}

func (f *fakeProjectRegistry) Get(_ context.Context, id string) (domain.Project, error) {
	p, ok := f.projects[id]
	if !ok {
		return domain.Project{}, domain.ErrProjectNotFound
	}
	return p, nil
}

func (f *fakeProjectRegistry) Archive(_ context.Context, _ string) error {
	return errors.New("fakeProjectRegistry.Archive not used by broker tests")
}

// fakeGitHubTokenBroker tracks every Mint invocation so tests can
// assert the broker was (or was not) called for a given grant case.
// It returns a deterministic synthetic token so AuditFingerprint
// expectations stay stable.
type fakeGitHubTokenBroker struct {
	calls         int
	lastReq       port.BrokerRequest
	lastActor     domain.BrokerActor
	returnedToken domain.InstallationToken
	returnedErr   error
}

func (f *fakeGitHubTokenBroker) Mint(_ context.Context, req port.BrokerRequest, actor domain.BrokerActor) (domain.InstallationToken, error) {
	f.calls++
	f.lastReq = req
	f.lastActor = actor
	return f.returnedToken, f.returnedErr
}

// activeProject is the canonical "everything is wired" Project record
// used by tests that should reach the broker.
func activeProject(id string) domain.Project {
	return domain.Project{
		ID:                      id,
		GitHubOrg:               "hironow",
		GitHubRepo:              id + "-repo",
		Status:                  domain.ProjectStatusActive,
		GitHubAppInstallationID: 12345,
	}
}

// Phonewave deny is enforced TWICE in the system: once at the domain
// grant matrix (broker_grant_test.go) and again here at the usecase
// layer. The second enforcement matters because the usecase is the
// only layer that wires the verified caller credential into the
// grant decision — a future refactor that bypasses domain.GrantPolicy
// would still be caught here.
func TestBrokerTokenService_Mint_PhonewaveDeniedForAllCallers(t *testing.T) {
	registry := &fakeProjectRegistry{projects: map[string]domain.Project{
		"proj-foo": activeProject("proj-foo"),
	}}
	broker := &fakeGitHubTokenBroker{}
	svc := usecase.NewBrokerTokenService(domain.DefaultGrantPolicy(), registry, broker)

	for _, caller := range domain.AllCallerTypes() {
		_, err := svc.Mint(
			context.Background(),
			port.BrokerRequest{ProjectID: "proj-foo", Tool: domain.ToolPhonewave},
			domain.BrokerActor{Type: caller},
		)
		if !errors.Is(err, domain.ErrToolNotPermitted) {
			t.Errorf("Mint(phonewave, %s) want ErrToolNotPermitted, got %v", caller, err)
		}
	}
	if broker.calls != 0 {
		t.Errorf("phonewave deny must short-circuit BEFORE the broker is called; got %d calls", broker.calls)
	}
}

// Per-project repo binding: an unknown project_id must fail with
// domain.ErrProjectNotFound (plan v8 §5.3). The broker never sees
// the request — registry failure short-circuits before mint.
func TestBrokerTokenService_Mint_UnknownProjectRejected(t *testing.T) {
	registry := &fakeProjectRegistry{projects: map[string]domain.Project{}}
	broker := &fakeGitHubTokenBroker{}
	svc := usecase.NewBrokerTokenService(domain.DefaultGrantPolicy(), registry, broker)

	_, err := svc.Mint(
		context.Background(),
		port.BrokerRequest{ProjectID: "missing", Tool: domain.ToolPaintress},
		domain.BrokerActor{Type: domain.CallerHumanOperator},
	)
	if !errors.Is(err, domain.ErrProjectNotFound) {
		t.Errorf("Mint(missing project) want ErrProjectNotFound, got %v", err)
	}
	if broker.calls != 0 {
		t.Errorf("unknown project must short-circuit BEFORE broker; got %d calls", broker.calls)
	}
}

// installation_id=0 is the registry's "no GitHub App installed for
// this project" sentinel (plan v8 §5.3). The broker cannot mint
// without it, so the use case rejects with
// usecase.ErrProjectInstallationMissing — distinct from
// ErrProjectNotFound because the project IS registered, just not
// bound to an installation.
func TestBrokerTokenService_Mint_ZeroInstallationIDRejected(t *testing.T) {
	p := activeProject("proj-no-app")
	p.GitHubAppInstallationID = 0
	registry := &fakeProjectRegistry{projects: map[string]domain.Project{"proj-no-app": p}}
	broker := &fakeGitHubTokenBroker{}
	svc := usecase.NewBrokerTokenService(domain.DefaultGrantPolicy(), registry, broker)

	_, err := svc.Mint(
		context.Background(),
		port.BrokerRequest{ProjectID: "proj-no-app", Tool: domain.ToolPaintress},
		domain.BrokerActor{Type: domain.CallerHumanOperator},
	)
	if !errors.Is(err, usecase.ErrProjectInstallationMissing) {
		t.Errorf("Mint(installation_id=0) want ErrProjectInstallationMissing, got %v", err)
	}
	if broker.calls != 0 {
		t.Errorf("installation_id=0 must short-circuit BEFORE broker; got %d calls", broker.calls)
	}
}

// Archived projects MUST be denied even when everything else is
// wired (codex review v7 #2 致命指摘). The lifecycle stop boundary
// belongs to the use case, not the registry.
func TestBrokerTokenService_Mint_ArchivedProjectRejected(t *testing.T) {
	p := activeProject("proj-archived")
	p.Status = domain.ProjectStatusArchived
	registry := &fakeProjectRegistry{projects: map[string]domain.Project{"proj-archived": p}}
	broker := &fakeGitHubTokenBroker{}
	svc := usecase.NewBrokerTokenService(domain.DefaultGrantPolicy(), registry, broker)

	_, err := svc.Mint(
		context.Background(),
		port.BrokerRequest{ProjectID: "proj-archived", Tool: domain.ToolPaintress},
		domain.BrokerActor{Type: domain.CallerHumanOperator},
	)
	if !errors.Is(err, usecase.ErrProjectNotActive) {
		t.Errorf("Mint(archived) want ErrProjectNotActive, got %v", err)
	}
	if broker.calls != 0 {
		t.Errorf("archived project must short-circuit BEFORE broker; got %d calls", broker.calls)
	}
}

// Happy path: every gate passes, the broker is called exactly once
// with (a) the verified actor untouched and (b) a BrokerRequest
// whose Tool / ProjectID match the input. The use case does not
// modify the request before forwarding it.
func TestBrokerTokenService_Mint_HappyPathCallsBrokerOnce(t *testing.T) {
	registry := &fakeProjectRegistry{projects: map[string]domain.Project{
		"proj-foo": activeProject("proj-foo"),
	}}
	expected := domain.InstallationToken{
		Token:     "ghs_synthetictesttoken",
		ProjectID: "proj-foo",
		Tool:      domain.ToolPaintress,
		Permissions: domain.RepositoryPermissions{
			Contents:     domain.PermWrite,
			PullRequests: domain.PermWrite,
		},
		Actor:            domain.BrokerActor{Type: domain.CallerHumanOperator, UserEmail: "x@y.example"},
		AuditFingerprint: domain.AuditFingerprint("ghs_synthetictesttoken"),
	}
	broker := &fakeGitHubTokenBroker{returnedToken: expected}
	svc := usecase.NewBrokerTokenService(domain.DefaultGrantPolicy(), registry, broker)

	got, err := svc.Mint(
		context.Background(),
		port.BrokerRequest{ProjectID: "proj-foo", Tool: domain.ToolPaintress},
		domain.BrokerActor{Type: domain.CallerHumanOperator, UserEmail: "x@y.example"},
	)
	if err != nil {
		t.Fatalf("Mint happy path returned error: %v", err)
	}
	if got.Token != expected.Token {
		t.Errorf("returned token = %q, want %q", got.Token, expected.Token)
	}
	if broker.calls != 1 {
		t.Errorf("broker.Mint must be called exactly once on happy path; got %d", broker.calls)
	}
	if broker.lastReq.ProjectID != "proj-foo" || broker.lastReq.Tool != domain.ToolPaintress {
		t.Errorf("broker received modified request: %+v", broker.lastReq)
	}
	if broker.lastActor.Type != domain.CallerHumanOperator || broker.lastActor.UserEmail != "x@y.example" {
		t.Errorf("broker received modified actor: %+v", broker.lastActor)
	}
}
