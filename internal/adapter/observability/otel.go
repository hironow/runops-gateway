// Package observability owns the OpenTelemetry TracerProvider lifecycle for
// every binary in this repo (cmd/server, cmd/dmail-receiver, cmd/dmail-emitter).
//
// Per ADR 0020 the export strategy is "direct OTLP gRPC, env-driven endpoint
// switching": local Jaeger v2 (localhost:4317) and prod Cloud Trace OTLP
// (telemetry.googleapis.com:443) read the same Go code.
//
// The "binary always boots" guarantee: SetupTracerProvider never errors on a
// missing endpoint. Dev macOS, CI, and unit-test runners commonly have no
// OTEL_EXPORTER_OTLP_ENDPOINT set; they must not crash on telemetry setup.
package observability

import (
	"context"
	"strings"

	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// Config carries the runtime configuration SetupTracerProvider needs. It is a
// plain struct (not env-coupled) so callers can either construct it from
// os.Getenv themselves or use ConfigFromEnv (added later).
type Config struct {
	// Endpoint is the OTLP gRPC endpoint, e.g. "localhost:4317" or
	// "telemetry.googleapis.com:443". Empty means "no exporter" — the
	// returned TracerProvider still satisfies the trace.TracerProvider API
	// but drops every span on the floor. This is the "binary always boots"
	// path.
	Endpoint string

	// ServiceName populates resource attribute service.name. It is the
	// minimum identification any backend needs.
	ServiceName string

	// ServiceVersion populates resource attribute service.version. Useful
	// for distinguishing canary vs stable revisions in a single trace
	// view. Build pipelines pass this via -ldflags '-X main.version=...'.
	ServiceVersion string
}

// serviceNamespace is the org-level grouping every binary in this repo
// reports. Hard-coded because there is one organization-style answer.
const serviceNamespace = "runops"

// BuildResource composes the OTel Resource that every span produced by this
// binary should carry. service.namespace is hard-coded; service.name and
// service.version come from cfg. The GCP resource detector is intentionally
// not wired here yet — it lives behind its own helper so unit tests do not
// hit the GCP metadata server.
func BuildResource(ctx context.Context, cfg Config) (*resource.Resource, error) {
	return resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(cfg.ServiceName),
			semconv.ServiceNamespace(serviceNamespace),
			semconv.ServiceVersion(cfg.ServiceVersion),
		),
	)
}

// SetupTracerProvider returns a TracerProvider that the caller is responsible
// for shutting down (defer tp.Shutdown(ctx)). With Endpoint empty, the
// returned provider has no exporter wired up — Shutdown still succeeds, all
// spans are silently dropped.
func SetupTracerProvider(ctx context.Context, cfg Config) (*sdktrace.TracerProvider, error) {
	_ = ctx
	_ = cfg
	return sdktrace.NewTracerProvider(), nil
}

// NormalizeEndpoint decodes a raw OTEL_EXPORTER_OTLP_ENDPOINT value into the
// (host:port, insecure) tuple the OTLP gRPC exporter wants.
//
// Returns ok=false for an empty input so callers can take the "no exporter"
// path. The OTel spec accepts the value with or without a scheme:
// "http://localhost:4317" → insecure (local Jaeger),
// "https://telemetry.googleapis.com:443" or bare "host:port" → secure
// (Cloud Trace).
func NormalizeEndpoint(raw string) (endpoint string, insecure bool, ok bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false, false
	}

	switch {
	case strings.HasPrefix(raw, "http://"):
		endpoint = strings.TrimPrefix(raw, "http://")
		insecure = true
	case strings.HasPrefix(raw, "https://"):
		endpoint = strings.TrimPrefix(raw, "https://")
		insecure = false
	default:
		endpoint = raw
		insecure = false
	}

	endpoint = strings.TrimRight(endpoint, "/")
	return endpoint, insecure, true
}
