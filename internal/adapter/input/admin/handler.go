// Package admin serves the registry CRUD HTTP endpoints under
// /admin/projects. Operators with the configured RUNOPS_ADMIN_TOKEN
// reach this surface; everyone else sees 401. The handler is opt-in
// at the composition root: when either the token or the registry is
// missing the routes are not registered at all (#0012, ADR 0030).
package admin

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/hironow/runops-gateway/internal/core/domain"
	"github.com/hironow/runops-gateway/internal/core/port"
)

// Handler serves the /admin/projects routes backed by a
// port.ProjectRegistry. All endpoints require Authorization: Bearer
// <token>. The token is held as []byte so subtle.ConstantTimeCompare
// can dodge timing attacks.
//
// writeDisabled gates the legacy mutation paths (= POST
// /admin/projects + POST /admin/projects/{id}/archive). When set,
// those endpoints respond with 410 Gone + Location: /rpc so operators
// migrate to the §B-5.2 JSON-RPC mutation methods. ADR 0040 §REST
// endpoint との関係 carry. GET endpoints stay open regardless.
type Handler struct {
	registry      port.ProjectRegistry
	token         []byte
	writeDisabled bool
}

// NewHandler builds an admin handler. The caller (cmd/server) is
// responsible for skipping registration when registry is nil or token
// is empty so the routes simply do not exist on those deployments.
func NewHandler(registry port.ProjectRegistry, token string) *Handler {
	return &Handler{registry: registry, token: []byte(token)}
}

// WithWriteDisabled marks the handler so the legacy mutation endpoints
// (= POST /admin/projects + POST /admin/projects/{id}/archive) respond
// with 410 Gone + Location: /rpc instead of mutating the registry. The
// builder pattern keeps the default constructor signature backwards-
// compatible so existing callers and tests need no edits.
func (h *Handler) WithWriteDisabled() *Handler {
	h.writeDisabled = true
	return h
}

// String redacts the configured token so accidental %v / Sprintf does
// not leak it into structured logs (#0012 ADR 0030).
func (h *Handler) String() string { return "admin.Handler{token=<redacted>}" }

// Register wires the four sub-handlers via http.ServeMux method+pattern
// style (Go 1.22+).
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /admin/projects", h.handleAdd)
	mux.HandleFunc("GET /admin/projects", h.handleList)
	mux.HandleFunc("GET /admin/projects/{id}", h.handleGet)
	mux.HandleFunc("POST /admin/projects/{id}/archive", h.handleArchive)
}

// authorize implements the strict Bearer-token spec from ADR 0030 §4:
// no TrimSpace anywhere, "Bearer" matched case-insensitively, exactly
// one space separator, claimed token rejected if it contains any
// whitespace or control character. constant-time compare guards the
// final equality check.
func (h *Handler) authorize(r *http.Request) bool {
	raw := r.Header.Get("Authorization")
	if len(raw) < 7 {
		return false
	}
	if !strings.EqualFold(raw[:6], "Bearer") || raw[6] != ' ' {
		return false
	}
	claimed := raw[7:]
	if claimed == "" {
		return false
	}
	if strings.IndexFunc(claimed, unicode.IsSpace) >= 0 ||
		strings.IndexFunc(claimed, unicode.IsControl) >= 0 {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(claimed), h.token) == 1
}

// requireAuth wraps the per-endpoint logic with the auth check. It
// emits a constant log message on failure — never the received header
// value or token bytes.
func (h *Handler) requireAuth(w http.ResponseWriter, r *http.Request, fn func()) {
	if !h.authorize(r) {
		slog.WarnContext(r.Context(), "admin: auth failed", "endpoint", r.URL.Path)
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	fn()
}

func (h *Handler) handleAdd(w http.ResponseWriter, r *http.Request) {
	h.requireAuth(w, r, func() {
		if h.writeDisabled {
			h.writeGoneToRPC(w, r)
			return
		}
		var p domain.Project
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		// Server-set fields: ignore whatever the client sent for these.
		p.Status = domain.ProjectStatusActive
		p.CreatedAt = time.Now().UTC()
		p.ArchivedAt = nil

		if err := h.registry.Add(r.Context(), p); err != nil {
			h.writeRegistryError(w, err)
			return
		}
		writeJSONProject(w, http.StatusCreated, p)
	})
}

// writeGoneToRPC emits 410 Gone with Location: /rpc so legacy clients
// migrate to the JSON-RPC mutation methods (= ADR 0040 §REST endpoint
// との関係). Constant log message; never echoes request body.
func (h *Handler) writeGoneToRPC(w http.ResponseWriter, r *http.Request) {
	slog.WarnContext(r.Context(),
		"admin: legacy write rejected (use /rpc)",
		"endpoint", r.URL.Path)
	w.Header().Set("Location", "/rpc")
	writeJSONError(w, http.StatusGone, "legacy write disabled, use POST /rpc")
}

func (h *Handler) handleList(w http.ResponseWriter, r *http.Request) {
	h.requireAuth(w, r, func() {
		filter, ok := parseStatusFilter(r.URL.Query().Get("status"))
		if !ok {
			writeJSONError(w, http.StatusBadRequest, "invalid status filter (want active|archived|all)")
			return
		}
		projects, err := h.registry.List(r.Context(), filter)
		if err != nil {
			h.writeRegistryError(w, err)
			return
		}
		// Initialise to non-nil so the JSON encoder emits [] instead of null.
		if projects == nil {
			projects = []domain.Project{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"projects": projects})
	})
}

func (h *Handler) handleGet(w http.ResponseWriter, r *http.Request) {
	h.requireAuth(w, r, func() {
		id := r.PathValue("id")
		p, err := h.registry.Get(r.Context(), id)
		if err != nil {
			h.writeRegistryError(w, err)
			return
		}
		writeJSONProject(w, http.StatusOK, p)
	})
}

func (h *Handler) handleArchive(w http.ResponseWriter, r *http.Request) {
	h.requireAuth(w, r, func() {
		if h.writeDisabled {
			h.writeGoneToRPC(w, r)
			return
		}
		id := r.PathValue("id")
		if err := h.registry.Archive(r.Context(), id); err != nil {
			h.writeRegistryError(w, err)
			return
		}
		// Re-fetch so the response shows the archived state.
		p, err := h.registry.Get(r.Context(), id)
		if err != nil {
			h.writeRegistryError(w, err)
			return
		}
		writeJSONProject(w, http.StatusOK, p)
	})
}

func parseStatusFilter(q string) (port.ProjectListFilter, bool) {
	switch strings.ToLower(strings.TrimSpace(q)) {
	case "", "all":
		return port.ProjectListFilter{}, true
	case "active":
		return port.ProjectListFilter{Status: domain.ProjectStatusActive}, true
	case "archived":
		return port.ProjectListFilter{Status: domain.ProjectStatusArchived}, true
	default:
		return port.ProjectListFilter{}, false
	}
}

// writeRegistryError maps domain sentinel errors to HTTP status codes.
// Other errors fall back to 500 with a generic message; the underlying
// error is logged at error level so the operator can grep for context
// without exposing internals to the caller.
func (h *Handler) writeRegistryError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrInvalidProjectID):
		writeJSONError(w, http.StatusBadRequest, "invalid project id")
	case errors.Is(err, domain.ErrProjectNotFound):
		writeJSONError(w, http.StatusNotFound, "project not found")
	case errors.Is(err, domain.ErrProjectAlreadyExists):
		writeJSONError(w, http.StatusConflict, "project already exists")
	default:
		slog.Error("admin: registry operation failed", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "registry operation failed")
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Error("admin: failed to encode response", "error", err)
	}
}

func writeJSONProject(w http.ResponseWriter, status int, p domain.Project) {
	writeJSON(w, status, map[string]domain.Project{"project": p})
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
