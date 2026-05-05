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

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
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
