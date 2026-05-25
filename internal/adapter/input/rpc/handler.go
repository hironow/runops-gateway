// Package rpc serves the JSON-RPC 2.0 transport endpoint at POST /rpc.
//
// Per ADR 0040 §HTTP transport, the handler enforces a strict two-layer
// model:
//
//   - transport-layer reject (= dispatcher 到達前) returns HTTP status:
//     405 (wrong method), 415 (wrong Content-Type), 401 (no/malformed
//     Authorization, registry miss).
//   - JSON-RPC layer reject (= parse error / unknown method / handler error)
//     returns HTTP 200 + JSON-RPC error envelope (= dispatcher decides).
//   - server-internal failure returns HTTP 500 + raw text.
//
// Authentication uses the multi-token admin registry (= ADR 0040 §identity
// contract). Bearer tokens are parsed with the strict ADR 0030 §4 rules
// (no TrimSpace, single-space separator, control-char reject) and looked up
// via a port.OperatorLookup adapter. On hit, the resolved domain.rpc.Operator
// is injected into the request context for downstream Method handlers.
package rpc

import (
	"encoding/json"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strings"
	"unicode"

	domainrpc "github.com/hironow/runops-gateway/internal/core/domain/rpc"
	"github.com/hironow/runops-gateway/internal/core/port"
	usecaserpc "github.com/hironow/runops-gateway/internal/usecase/rpc"
)

// maxRequestBodyBytes bounds incoming request bodies (= rough DoS guard).
// JSON-RPC envelopes for admin methods are small; 1 MiB is generous.
const maxRequestBodyBytes = 1 << 20

// Handler is the http.Handler that bridges HTTP and the JSON-RPC dispatcher.
//
// transport is the routing core (usually *usecaserpc.Dispatcher); lookup
// resolves Bearer tokens to operators.
type Handler struct {
	transport port.RPCTransport
	lookup    port.OperatorLookup
}

// NewHandler builds the /rpc handler. Both arguments must be non-nil; the
// caller (composition root) is responsible for skipping registration when
// either is missing.
func NewHandler(transport port.RPCTransport, lookup port.OperatorLookup) *Handler {
	if transport == nil || lookup == nil {
		panic("rpc.NewHandler: transport and lookup must be non-nil")
	}
	return &Handler{transport: transport, lookup: lookup}
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// transport-layer reject 1: HTTP method
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// transport-layer reject 2: Content-Type (= application/json with optional params)
	if !isJSONContentType(r.Header.Get("Content-Type")) {
		http.Error(w, "unsupported media type", http.StatusUnsupportedMediaType)
		return
	}
	// transport-layer reject 3: Authorization
	op, ok := h.authenticate(r)
	if !ok {
		// constant log message; never echo the received header value.
		slog.WarnContext(r.Context(), "rpc: auth failed", "endpoint", r.URL.Path)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// dispatcher to JSON-RPC layer.
	body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBodyBytes+1))
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if len(body) > maxRequestBodyBytes {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return
	}

	ctx := usecaserpc.WithOperator(r.Context(), op)
	resp, serveErr := h.transport.ServeRPC(ctx, body)
	if serveErr != nil {
		// server-internal failure (e.g., response marshal bug).
		slog.ErrorContext(ctx, "rpc: ServeRPC failed", "error", serveErr)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	// resp is a pre-marshaled JSON-RPC envelope produced by dispatcher.
	// Wrap as json.RawMessage and route through the encoder so the
	// transport layer never raw-writes bytes (= keeps the codepath aligned
	// with the project-wide JSON-only output rule).
	if err := json.NewEncoder(w).Encode(json.RawMessage(resp)); err != nil {
		// client disconnected mid-write; nothing to do.
		slog.DebugContext(ctx, "rpc: write response failed", "error", err)
	}
}

// authenticate parses the Authorization header per ADR 0030 §4 strict rules
// and resolves the bearer token via the multi-token registry.
func (h *Handler) authenticate(r *http.Request) (domainrpc.Operator, bool) {
	raw := r.Header.Get("Authorization")
	token, ok := parseStrictBearer(raw)
	if !ok {
		return domainrpc.Operator{}, false
	}
	op, found := h.lookup.Lookup(token)
	if !found {
		return domainrpc.Operator{}, false
	}
	return op, true
}

// parseStrictBearer applies ADR 0030 §4 rules:
//   - exactly "Bearer<sp>token" with a single ASCII space
//   - "Bearer" matched case-insensitively (= RFC 6750 § 2.1)
//   - token must be non-empty and contain no whitespace or control chars
func parseStrictBearer(raw string) (string, bool) {
	if len(raw) < 7 {
		return "", false
	}
	if !strings.EqualFold(raw[:6], "Bearer") || raw[6] != ' ' {
		return "", false
	}
	token := raw[7:]
	if token == "" {
		return "", false
	}
	if strings.IndexFunc(token, unicode.IsSpace) >= 0 ||
		strings.IndexFunc(token, unicode.IsControl) >= 0 {
		return "", false
	}
	return token, true
}

// isJSONContentType returns true for application/json (with optional MIME
// parameters such as charset). Any other media type is rejected with 415.
func isJSONContentType(header string) bool {
	if header == "" {
		// JSON-RPC over HTTP without Content-Type header is non-conforming;
		// reject explicitly.
		return false
	}
	mt, _, err := mime.ParseMediaType(header)
	if err != nil {
		return false
	}
	return strings.EqualFold(mt, "application/json")
}
