package observability_test

import (
	"context"
	"testing"

	"github.com/hironow/runops-gateway/internal/adapter/observability"
)

// TestSetupTracerProvider_NoEndpointReturnsUsableProvider verifies the
// "binary always boots" guarantee from ADR 0020: when OTEL_EXPORTER_OTLP_ENDPOINT
// is unset (empty Config.Endpoint), SetupTracerProvider must still succeed and
// return a TracerProvider whose Shutdown is callable. Production binaries call
// `defer tp.Shutdown(ctx)` unconditionally; dev/CI/test environments that have
// no Jaeger or Cloud Trace reachable must not crash because of telemetry setup.
func TestSetupTracerProvider_NoEndpointReturnsUsableProvider(t *testing.T) {
	// given
	ctx := context.Background()
	cfg := observability.Config{
		ServiceName: "runops-gateway",
		// Endpoint intentionally empty — simulates dev macOS / unit test runner
		// without OTEL_EXPORTER_OTLP_ENDPOINT set.
	}

	// when
	tp, err := observability.SetupTracerProvider(ctx, cfg)

	// then
	if err != nil {
		t.Fatalf("SetupTracerProvider returned error for empty endpoint: %v", err)
	}
	if tp == nil {
		t.Fatalf("SetupTracerProvider returned nil TracerProvider")
	}
	if err := tp.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown returned error: %v", err)
	}
}
