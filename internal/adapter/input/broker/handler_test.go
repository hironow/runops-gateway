package broker_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hironow/runops-gateway/internal/adapter/input/broker"
	"github.com/hironow/runops-gateway/internal/core/domain"
	"github.com/hironow/runops-gateway/internal/core/port"
	"github.com/hironow/runops-gateway/internal/usecase"
)

// fakeBrokerService satisfies broker.BrokerService and records every
// invocation. Returning either a token or an error per test case
// covers the handler's success / error mapping branches without
// pulling the real BrokerTokenService into the test.
type fakeBrokerService struct {
	calls    int
	gotReq   port.BrokerRequest
	gotActor domain.BrokerActor
	tok      domain.InstallationToken
	err      error
}

func (f *fakeBrokerService) Mint(_ context.Context, req port.BrokerRequest, actor domain.BrokerActor) (domain.InstallationToken, error) {
	f.calls++
	f.gotReq = req
	f.gotActor = actor
	return f.tok, f.err
}

// fakeAuthenticator stands in for the Phase 3b verifier chain.
// It returns whatever BrokerActor / error the test pre-configures
// so the handler's error-mapping branches can be exercised.
type fakeAuthenticator struct {
	actor domain.BrokerActor
	err   error
}

func (f *fakeAuthenticator) Authenticate(_ *http.Request, _ string, _ domain.Tool) (domain.BrokerActor, error) {
	return f.actor, f.err
}

func newHandler(svc broker.BrokerService, auth broker.Authenticator) http.Handler {
	return broker.NewHandler(svc, auth)
}

func freshActor() domain.BrokerActor {
	return domain.BrokerActor{Type: domain.CallerHumanOperator, UserEmail: "x@y.example"}
}

// Happy path: the handler authenticates the caller, validates the
// request body, mints a token via the service, and returns the
// JSON response from plan v8 §5.5.
func TestHandler_PostBrokerToken_HappyPathReturnsJSON(t *testing.T) {
	expected := domain.InstallationToken{
		Token:            "ghs_synthetic",
		ExpiresAt:        time.Now().Add(50 * time.Minute).UTC().Truncate(time.Second),
		Actor:            freshActor(),
		ProjectID:        "proj-foo",
		Tool:             domain.ToolPaintress,
		Permissions:      domain.RepositoryPermissions{Contents: domain.PermWrite, PullRequests: domain.PermWrite},
		AuditFingerprint: domain.AuditFingerprint("ghs_synthetic"),
	}
	svc := &fakeBrokerService{tok: expected}
	auth := &fakeAuthenticator{actor: freshActor()}
	h := newHandler(svc, auth)

	body := strings.NewReader(`{"project_id":"proj-foo","tool":"paintress"}`)
	req := httptest.NewRequest(http.MethodPost, "/broker/token", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var got domain.InstallationToken
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Token != expected.Token || got.AuditFingerprint != expected.AuditFingerprint {
		t.Errorf("response token = %+v, want %+v", got, expected)
	}
	if svc.calls != 1 {
		t.Errorf("service.Mint called %d times, want 1", svc.calls)
	}
	if svc.gotReq.ProjectID != "proj-foo" || svc.gotReq.Tool != domain.ToolPaintress {
		t.Errorf("service received wrong request: %+v", svc.gotReq)
	}
	if svc.gotActor != freshActor() {
		t.Errorf("service received wrong actor: %+v", svc.gotActor)
	}
}

// Non-POST methods must be rejected with 405 and the service must
// not be called.
func TestHandler_NonPostReturns405(t *testing.T) {
	svc := &fakeBrokerService{}
	auth := &fakeAuthenticator{actor: freshActor()}
	h := newHandler(svc, auth)
	for _, m := range []string{http.MethodGet, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		req := httptest.NewRequest(m, "/broker/token", nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("method %s: status = %d, want 405", m, rr.Code)
		}
	}
	if svc.calls != 0 {
		t.Errorf("service must not be called for non-POST; got %d", svc.calls)
	}
}

// Malformed JSON body produces 400 (not 500) so the caller can
// distinguish parse failures from upstream errors.
func TestHandler_MalformedJSONReturns400(t *testing.T) {
	svc := &fakeBrokerService{}
	auth := &fakeAuthenticator{actor: freshActor()}
	h := newHandler(svc, auth)
	req := httptest.NewRequest(http.MethodPost, "/broker/token", strings.NewReader(`{not-json`))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

// Unknown fields → 400 (caller bug). The handler delegates to
// domain.ValidateBrokerRequest, so the distinction between unknown
// (400) and known-escalation (403) is made at the domain layer.
func TestHandler_UnknownFieldReturns400(t *testing.T) {
	svc := &fakeBrokerService{}
	auth := &fakeAuthenticator{actor: freshActor()}
	h := newHandler(svc, auth)
	req := httptest.NewRequest(http.MethodPost, "/broker/token", strings.NewReader(`{"project_id":"p","tool":"paintress","misspelt":"x"}`))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
	if svc.calls != 0 {
		t.Errorf("service must NOT be called when validation fails; got %d", svc.calls)
	}
}

// Caller-supplied escalation fields → 403. Per plan v8 §5.4 these
// MUST be distinguishable from 400 in the audit log so the gateway
// can surface attack-shaped attempts.
func TestHandler_EscalationFieldReturns403(t *testing.T) {
	svc := &fakeBrokerService{}
	auth := &fakeAuthenticator{actor: freshActor()}
	h := newHandler(svc, auth)
	req := httptest.NewRequest(http.MethodPost, "/broker/token", strings.NewReader(`{"project_id":"p","tool":"paintress","installation_id":12345}`))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403; body=%s", rr.Code, rr.Body.String())
	}
}

// Authenticator failure → 401. The handler must NOT consult the
// service when authentication fails — caller identity is the
// foundation of every grant decision.
func TestHandler_AuthenticationFailureReturns401(t *testing.T) {
	svc := &fakeBrokerService{}
	auth := &fakeAuthenticator{err: errors.New("synthetic auth fail")}
	h := newHandler(svc, auth)
	req := httptest.NewRequest(http.MethodPost, "/broker/token", strings.NewReader(`{"project_id":"p","tool":"paintress"}`))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401; body=%s", rr.Code, rr.Body.String())
	}
	if svc.calls != 0 {
		t.Errorf("service must NOT be called when auth fails; got %d", svc.calls)
	}
}

// Service-side grant-matrix violation → 403 (e.g. phonewave deny).
func TestHandler_ServiceErrToolNotPermittedReturns403(t *testing.T) {
	svc := &fakeBrokerService{err: domain.ErrToolNotPermitted}
	auth := &fakeAuthenticator{actor: freshActor()}
	h := newHandler(svc, auth)
	req := httptest.NewRequest(http.MethodPost, "/broker/token", strings.NewReader(`{"project_id":"p","tool":"phonewave"}`))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403; body=%s", rr.Code, rr.Body.String())
	}
}

// Project not found → 404.
func TestHandler_ServiceErrProjectNotFoundReturns404(t *testing.T) {
	svc := &fakeBrokerService{err: domain.ErrProjectNotFound}
	auth := &fakeAuthenticator{actor: freshActor()}
	h := newHandler(svc, auth)
	req := httptest.NewRequest(http.MethodPost, "/broker/token", strings.NewReader(`{"project_id":"missing","tool":"paintress"}`))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404; body=%s", rr.Code, rr.Body.String())
	}
}

// Project not active / installation missing → 422 Unprocessable
// Entity (server understands but cannot satisfy because of project
// state).
func TestHandler_ServiceProjectStateErrorsReturn422(t *testing.T) {
	for name, svcErr := range map[string]error{
		"not active":           usecase.ErrProjectNotActive,
		"installation missing": usecase.ErrProjectInstallationMissing,
	} {
		svc := &fakeBrokerService{err: svcErr}
		auth := &fakeAuthenticator{actor: freshActor()}
		h := newHandler(svc, auth)
		req := httptest.NewRequest(http.MethodPost, "/broker/token", strings.NewReader(`{"project_id":"p","tool":"paintress"}`))
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnprocessableEntity {
			t.Errorf("[%s] status = %d, want 422; body=%s", name, rr.Code, rr.Body.String())
		}
	}
}

// Generic upstream error → 502 Bad Gateway. The broker is "the
// gateway" between callers and the upstream GitHub App API; opaque
// upstream failures must surface as 502 so callers can distinguish
// transport faults from request-shape faults.
func TestHandler_ServiceUnknownErrorReturns502(t *testing.T) {
	svc := &fakeBrokerService{err: errors.New("synthetic upstream 500")}
	auth := &fakeAuthenticator{actor: freshActor()}
	h := newHandler(svc, auth)
	req := httptest.NewRequest(http.MethodPost, "/broker/token", strings.NewReader(`{"project_id":"p","tool":"paintress"}`))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502; body=%s", rr.Code, rr.Body.String())
	}
}

// Response body never contains the raw token in any error path —
// only the success path emits the token. We exercise a 4xx + 5xx
// to make sure the error responses are token-free (a leak here
// would defeat plan v8 §5.5).
func TestHandler_ErrorResponseBodyDoesNotContainToken(t *testing.T) {
	for name, svcErr := range map[string]error{
		"403 grant":    domain.ErrToolNotPermitted,
		"404 missing":  domain.ErrProjectNotFound,
		"502 upstream": errors.New("upstream"),
	} {
		svc := &fakeBrokerService{err: svcErr}
		auth := &fakeAuthenticator{actor: freshActor()}
		h := newHandler(svc, auth)
		req := httptest.NewRequest(http.MethodPost, "/broker/token", strings.NewReader(`{"project_id":"p","tool":"paintress"}`))
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		body, _ := io.ReadAll(rr.Body)
		if strings.Contains(strings.ToLower(string(body)), "ghs_") {
			t.Errorf("[%s] error body must not contain ghs_ token-shaped material; got %q", name, body)
		}
	}
}
