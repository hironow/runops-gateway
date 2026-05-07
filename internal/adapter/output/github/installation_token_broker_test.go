package github

import (
	"context"
	"errors"
	"testing"
	"time"

	gogh "github.com/google/go-github/v66/github"

	"github.com/hironow/runops-gateway/internal/core/domain"
	"github.com/hironow/runops-gateway/internal/core/port"
)

// fakeProjectRegistry is the test double pattern from Phase 1b
// (internal/usecase/broker_token_test.go). Re-declared here because
// only tests/utils/ is the importable test helper directory.
type fakeProjectRegistry struct {
	projects map[string]domain.Project
}

func (f *fakeProjectRegistry) Add(_ context.Context, _ domain.Project) error {
	return errors.New("fakeProjectRegistry.Add not used")
}

func (f *fakeProjectRegistry) List(_ context.Context, _ port.ProjectListFilter) ([]domain.Project, error) {
	return nil, errors.New("fakeProjectRegistry.List not used")
}

func (f *fakeProjectRegistry) Get(_ context.Context, id string) (domain.Project, error) {
	p, ok := f.projects[id]
	if !ok {
		return domain.Project{}, domain.ErrProjectNotFound
	}
	return p, nil
}

func (f *fakeProjectRegistry) Archive(_ context.Context, _ string) error {
	return errors.New("fakeProjectRegistry.Archive not used")
}

// fakeMinter records every call so per-project repo binding and
// permission narrowing can be asserted at the API boundary.
type fakeMinter struct {
	calls       int
	lastInstall int64
	lastOpts    *gogh.InstallationTokenOptions
	returnTok   *gogh.InstallationToken
	returnErr   error
}

func (m *fakeMinter) Mint(_ context.Context, installationID int64, opts *gogh.InstallationTokenOptions) (*gogh.InstallationToken, error) {
	m.calls++
	m.lastInstall = installationID
	m.lastOpts = opts
	return m.returnTok, m.returnErr
}

func project(id, repo string, installationID int64) domain.Project {
	return domain.Project{
		ID:                      id,
		GitHubOrg:               "hironow",
		GitHubRepo:              repo,
		Status:                  domain.ProjectStatusActive,
		GitHubAppInstallationID: installationID,
	}
}

func ghToken(s string, expiresAt time.Time) *gogh.InstallationToken {
	expTS := gogh.Timestamp{Time: expiresAt}
	return &gogh.InstallationToken{Token: &s, ExpiresAt: &expTS}
}

// Happy path: paintress request → minter receives correct
// installation_id + single-repo binding + write permissions; broker
// returns a domain.InstallationToken with all fields populated and
// AuditFingerprint computed.
func TestInstallationTokenBroker_Mint_HappyPathProducesScopedToken(t *testing.T) {
	registry := &fakeProjectRegistry{projects: map[string]domain.Project{
		"proj-foo": project("proj-foo", "foo-app", 12345),
	}}
	expires := time.Now().Add(50 * time.Minute).UTC().Truncate(time.Second)
	minter := &fakeMinter{returnTok: ghToken("ghs_synthetic", expires)}
	broker := newInstallationTokenBroker(minter, registry, domain.DefaultGrantPolicy())

	got, err := broker.Mint(
		context.Background(),
		port.BrokerRequest{ProjectID: "proj-foo", Tool: domain.ToolPaintress},
		domain.BrokerActor{Type: domain.CallerHumanOperator, UserEmail: "x@y.example"},
	)
	if err != nil {
		t.Fatalf("Mint happy path error: %v", err)
	}

	if minter.calls != 1 {
		t.Errorf("minter.Mint called %d times, want 1", minter.calls)
	}
	if minter.lastInstall != 12345 {
		t.Errorf("minter received installation_id %d, want 12345", minter.lastInstall)
	}
	if got.Token != "ghs_synthetic" {
		t.Errorf("returned token = %q, want ghs_synthetic", got.Token)
	}
	if !got.ExpiresAt.Equal(expires) {
		t.Errorf("ExpiresAt = %v, want %v", got.ExpiresAt, expires)
	}
	if got.ProjectID != "proj-foo" || got.Tool != domain.ToolPaintress {
		t.Errorf("response project/tool fields wrong: %+v", got)
	}
	if got.Actor.UserEmail != "x@y.example" {
		t.Errorf("actor not propagated: %+v", got.Actor)
	}
	wantFp := domain.AuditFingerprint("ghs_synthetic")
	if got.AuditFingerprint != wantFp {
		t.Errorf("AuditFingerprint = %q, want %q", got.AuditFingerprint, wantFp)
	}
}

// Per-project repo binding (plan v8 §5.3 codex v6 致命指摘): the
// minter MUST receive exactly one repository, and it MUST be the
// repo bound to the project_id in the registry. Even a multi-entry
// list of valid repos would soften the boundary the broker enforces.
func TestInstallationTokenBroker_Mint_PerProjectRepoBindingOnlyOneRepo(t *testing.T) {
	registry := &fakeProjectRegistry{projects: map[string]domain.Project{
		"proj-foo": project("proj-foo", "foo-app", 12345),
	}}
	minter := &fakeMinter{returnTok: ghToken("ghs_x", time.Now().Add(50*time.Minute))}
	broker := newInstallationTokenBroker(minter, registry, domain.DefaultGrantPolicy())

	_, err := broker.Mint(
		context.Background(),
		port.BrokerRequest{ProjectID: "proj-foo", Tool: domain.ToolPaintress},
		domain.BrokerActor{Type: domain.CallerHumanOperator},
	)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if got := len(minter.lastOpts.Repositories); got != 1 {
		t.Errorf("Repositories has %d entries, want exactly 1 (per-project binding)", got)
	}
	if minter.lastOpts.Repositories[0] != "foo-app" {
		t.Errorf("Repositories[0] = %q, want foo-app", minter.lastOpts.Repositories[0])
	}
}

// Read-only tool (sightjack): Permissions.Contents must be "read",
// Permissions.PullRequests must be NIL pointer (= GitHub treats the
// field as unset, narrowing the token to read-only contents).
func TestInstallationTokenBroker_Mint_ReadOnlyToolHasNoWritePermission(t *testing.T) {
	registry := &fakeProjectRegistry{projects: map[string]domain.Project{
		"proj-foo": project("proj-foo", "foo-app", 12345),
	}}
	minter := &fakeMinter{returnTok: ghToken("ghs_y", time.Now().Add(50*time.Minute))}
	broker := newInstallationTokenBroker(minter, registry, domain.DefaultGrantPolicy())

	_, err := broker.Mint(
		context.Background(),
		port.BrokerRequest{ProjectID: "proj-foo", Tool: domain.ToolSightjack},
		domain.BrokerActor{Type: domain.CallerAIAgent},
	)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	perms := minter.lastOpts.Permissions
	if perms == nil || perms.Contents == nil || *perms.Contents != "read" {
		t.Errorf("sightjack must request contents:read, got %+v", perms)
	}
	if perms.PullRequests != nil {
		t.Errorf("sightjack must NOT request pull_requests permission; got %+v", perms.PullRequests)
	}
}

// Phonewave is denied at the grant matrix layer BEFORE the minter
// is consulted. The broker is the second-line of defence that
// re-checks the matrix even though the use case already did.
func TestInstallationTokenBroker_Mint_PhonewaveRejectedBeforeMinter(t *testing.T) {
	registry := &fakeProjectRegistry{projects: map[string]domain.Project{
		"proj-foo": project("proj-foo", "foo-app", 12345),
	}}
	minter := &fakeMinter{}
	broker := newInstallationTokenBroker(minter, registry, domain.DefaultGrantPolicy())

	_, err := broker.Mint(
		context.Background(),
		port.BrokerRequest{ProjectID: "proj-foo", Tool: domain.ToolPhonewave},
		domain.BrokerActor{Type: domain.CallerHumanOperator},
	)
	if !errors.Is(err, domain.ErrToolNotPermitted) {
		t.Errorf("phonewave Mint want ErrToolNotPermitted, got %v", err)
	}
	if minter.calls != 0 {
		t.Errorf("phonewave deny must short-circuit BEFORE the minter; got %d calls", minter.calls)
	}
}

// Project lookup failure (e.g. unknown project_id) propagates the
// registry error untouched so the use case / handler can render
// the right 4xx.
func TestInstallationTokenBroker_Mint_ProjectNotFoundPropagates(t *testing.T) {
	registry := &fakeProjectRegistry{projects: map[string]domain.Project{}}
	minter := &fakeMinter{}
	broker := newInstallationTokenBroker(minter, registry, domain.DefaultGrantPolicy())

	_, err := broker.Mint(
		context.Background(),
		port.BrokerRequest{ProjectID: "missing", Tool: domain.ToolPaintress},
		domain.BrokerActor{Type: domain.CallerHumanOperator},
	)
	if !errors.Is(err, domain.ErrProjectNotFound) {
		t.Errorf("unknown project want ErrProjectNotFound, got %v", err)
	}
	if minter.calls != 0 {
		t.Errorf("registry miss must short-circuit; got %d minter calls", minter.calls)
	}
}

// Upstream API failure surfaces unchanged so the use case can
// distinguish transport failures from grant-matrix / registry rejections.
func TestInstallationTokenBroker_Mint_MinterErrorPropagates(t *testing.T) {
	registry := &fakeProjectRegistry{projects: map[string]domain.Project{
		"proj-foo": project("proj-foo", "foo-app", 12345),
	}}
	wantErr := errors.New("synthetic upstream 500")
	minter := &fakeMinter{returnErr: wantErr}
	broker := newInstallationTokenBroker(minter, registry, domain.DefaultGrantPolicy())

	_, err := broker.Mint(
		context.Background(),
		port.BrokerRequest{ProjectID: "proj-foo", Tool: domain.ToolPaintress},
		domain.BrokerActor{Type: domain.CallerHumanOperator},
	)
	if !errors.Is(err, wantErr) {
		t.Errorf("upstream error want %v, got %v", wantErr, err)
	}
}
