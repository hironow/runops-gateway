// Package broker implements the HTTP handler for POST /broker/token
// (refs#0007 plan v8 §5.4 / §5.5 / §6 step 17). The handler is the
// HTTP-facing boundary that:
//
//  1. Authenticates the inbound caller via the Authenticator
//     (Phase 3b plugs in the 4-verifier chain).
//  2. Validates the request body against domain.ValidateBrokerRequest
//     (= plan v8 §5.4 schema lockdown — caller-supplied escalation
//     fields → 403, unknown fields → 400).
//  3. Forwards a sanitised port.BrokerRequest + verified
//     domain.BrokerActor into the BrokerService (= the use case).
//  4. Maps the use case's typed errors to the appropriate HTTP
//     status code and writes the §5.5 JSON response on success.
//
// The handler never logs the raw token — only the success response
// body emits it once. Every error path returns a token-free message
// (see TestHandler_ErrorResponseBodyDoesNotContainToken).
package broker

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/hironow/runops-gateway/internal/core/domain"
	"github.com/hironow/runops-gateway/internal/core/port"
	"github.com/hironow/runops-gateway/internal/usecase"
)

// BrokerService is the inbound dependency the HTTP handler needs.
// usecase.BrokerTokenService satisfies this; the seam keeps the
// handler unit-testable without importing the concrete service.
type BrokerService interface {
	Mint(ctx context.Context, req port.BrokerRequest, actor domain.BrokerActor) (domain.InstallationToken, error)
}

// Authenticator turns the inbound HTTP request into a verified
// domain.BrokerActor. Phase 3a accepts any Authenticator;
// Phase 3b wires the 4-caller chain (cloudrun_iam /
// workload_identity / gcloud_identity / delegated_agent).
type Authenticator interface {
	Authenticate(r *http.Request) (domain.BrokerActor, error)
}

// Handler is the http.Handler for POST /broker/token.
type Handler struct {
	svc  BrokerService
	auth Authenticator
}

// NewHandler constructs the handler with its two dependencies.
func NewHandler(svc BrokerService, auth Authenticator) *Handler {
	return &Handler{svc: svc, auth: auth}
}

// requestPayload is the typed view of the §5.4 allow-listed fields.
// Only project_id / tool / session_id arrive here; every other
// known field is rejected by domain.ValidateBrokerRequest before
// the typed decode happens.
type requestPayload struct {
	ProjectID string `json:"project_id"`
	Tool      string `json:"tool"`
	SessionID string `json:"session_id,omitempty"`
}

// ServeHTTP implements http.Handler. The flow is intentionally
// linear so the security-critical ordering (auth → validate →
// service → respond) is obvious to a reviewer.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "cannot read request body")
		return
	}

	var raw map[string]any
	if err := json.Unmarshal(bodyBytes, &raw); err != nil {
		writeError(w, http.StatusBadRequest, "request body is not valid JSON")
		return
	}

	if err := domain.ValidateBrokerRequest(raw); err != nil {
		switch {
		case errors.Is(err, domain.ErrRequestSchemaViolation):
			slog.Warn("broker request contains escalation field",
				"audit_event", "broker_escalation_attempt",
				"path", r.URL.Path,
				"remote_addr", r.RemoteAddr)
			writeError(w, http.StatusForbidden, "request contains caller-supplied escalation field")
		case errors.Is(err, domain.ErrUnknownRequestField):
			writeError(w, http.StatusBadRequest, "request contains unknown field")
		default:
			writeError(w, http.StatusBadRequest, "request validation failed")
		}
		return
	}

	var typed requestPayload
	if err := json.Unmarshal(bodyBytes, &typed); err != nil {
		writeError(w, http.StatusBadRequest, "request body cannot be decoded")
		return
	}

	actor, err := h.auth.Authenticate(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "authentication failed")
		return
	}

	tok, err := h.svc.Mint(r.Context(), port.BrokerRequest{
		ProjectID: typed.ProjectID,
		Tool:      domain.Tool(typed.Tool),
		SessionID: typed.SessionID,
	}, actor)
	if err != nil {
		h.writeServiceError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(tok); err != nil {
		// At this point the response status is already 200; encoding
		// failure is logged but cannot be surfaced to the caller.
		slog.Error("broker response encode failed", "err", err)
	}
}

// writeServiceError maps use-case-layer errors to HTTP status codes
// per plan v8 §5.4 / §5.5. Unknown errors fall back to 502 because
// the broker is, semantically, the gateway between the caller and
// the upstream GitHub App API; opaque upstream failures are
// transport faults from the caller's point of view.
func (h *Handler) writeServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrToolNotPermitted):
		writeError(w, http.StatusForbidden, "tool not permitted for this caller")
	case errors.Is(err, domain.ErrProjectNotFound):
		writeError(w, http.StatusNotFound, "project not found")
	case errors.Is(err, usecase.ErrProjectNotActive):
		writeError(w, http.StatusUnprocessableEntity, "project is not active")
	case errors.Is(err, usecase.ErrProjectInstallationMissing):
		writeError(w, http.StatusUnprocessableEntity, "project has no GitHub App installation bound")
	default:
		writeError(w, http.StatusBadGateway, "upstream token mint failed")
	}
}

// writeError writes a JSON {"error": "<msg>"} body. The message
// MUST NOT contain any token-derived material — the success path is
// the only place the raw token leaves the broker.
func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
