package observability_test

import (
	"context"
	"testing"

	"github.com/hironow/runops-gateway/internal/adapter/observability"
)

// TestNormalizeEndpoint_StripsSchemeAndDecidesInsecure exercises the pure
// helper that decodes OTEL_EXPORTER_OTLP_ENDPOINT into the (host:port,
// insecure) tuple the OTLP gRPC exporter wants. The OTel spec accepts
// values with or without a scheme; "http://" implies insecure (local
// Jaeger), "https://" or bare host:port implies secure (Cloud Trace).
//
// We isolate this in a pure function so the rest of SetupTracerProvider
// (network dial, resource detection) does not have to be touched to
// regression-test scheme handling.
func TestNormalizeEndpoint_StripsSchemeAndDecidesInsecure(t *testing.T) {
	cases := []struct {
		name         string
		raw          string
		wantEndpoint string
		wantInsecure bool
		wantOK       bool
	}{
		{
			name:         "empty input means no exporter",
			raw:          "",
			wantEndpoint: "",
			wantInsecure: false,
			wantOK:       false,
		},
		{
			name:         "bare host:port defaults to secure (Cloud Trace style)",
			raw:          "telemetry.googleapis.com:443",
			wantEndpoint: "telemetry.googleapis.com:443",
			wantInsecure: false,
			wantOK:       true,
		},
		{
			name:         "http scheme implies insecure (local Jaeger v2)",
			raw:          "http://localhost:4317",
			wantEndpoint: "localhost:4317",
			wantInsecure: true,
			wantOK:       true,
		},
		{
			name:         "https scheme stays secure",
			raw:          "https://telemetry.googleapis.com:443",
			wantEndpoint: "telemetry.googleapis.com:443",
			wantInsecure: false,
			wantOK:       true,
		},
		{
			name:         "trailing slash trimmed",
			raw:          "http://localhost:4317/",
			wantEndpoint: "localhost:4317",
			wantInsecure: true,
			wantOK:       true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, insecure, ok := observability.NormalizeEndpoint(tc.raw)
			if ok != tc.wantOK {
				t.Errorf("ok = %v, want %v", ok, tc.wantOK)
			}
			if got != tc.wantEndpoint {
				t.Errorf("endpoint = %q, want %q", got, tc.wantEndpoint)
			}
			if insecure != tc.wantInsecure {
				t.Errorf("insecure = %v, want %v", insecure, tc.wantInsecure)
			}
		})
	}
}

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
