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

// TestBuildResource_PutsServiceAttributesOnTheResource verifies the resource
// builder embeds the configured service identification (service.name,
// service.namespace, service.version) so every span the provider emits is
// attributable to the right binary.
//
// We deliberately skip the GCP detector here — that one calls into the
// metadata server and is exercised separately. This test isolates the part
// the caller controls (Config -> resource attributes).
func TestBuildResource_PutsServiceAttributesOnTheResource(t *testing.T) {
	// given
	ctx := context.Background()
	cfg := observability.Config{
		ServiceName:    "runops-gateway",
		ServiceVersion: "v0.4.1+phase4a",
	}

	// when
	res, err := observability.BuildResource(ctx, cfg)
	if err != nil {
		t.Fatalf("BuildResource returned error: %v", err)
	}

	// then
	got := map[string]string{}
	for _, kv := range res.Attributes() {
		got[string(kv.Key)] = kv.Value.AsString()
	}
	if got["service.name"] != "runops-gateway" {
		t.Errorf("service.name = %q, want %q", got["service.name"], "runops-gateway")
	}
	if got["service.namespace"] != "runops" {
		t.Errorf("service.namespace = %q, want %q", got["service.namespace"], "runops")
	}
	if got["service.version"] != "v0.4.1+phase4a" {
		t.Errorf("service.version = %q, want %q", got["service.version"], "v0.4.1+phase4a")
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
