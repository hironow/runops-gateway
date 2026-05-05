package observability_test

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/hironow/runops-gateway/internal/adapter/observability"
)

// TestSetupTracerProvider_DialsConfiguredEndpoint exercises the actual export
// path: when Config.Endpoint points at a reachable host:port (with insecure
// mode), SetupTracerProvider must succeed without blocking on the dial AND
// the OTLP gRPC client must eventually try to connect to that endpoint.
//
// The test uses a plain net.Listener as a "dial detector" — it accepts and
// then silently drops the connection. We do not spin up a full gRPC server
// here because the unit-of-work is "did SetupTracerProvider wire the
// exporter to *this* address?", not "did spans round-trip end-to-end". That
// fuller assertion belongs in an integration test.
func TestSetupTracerProvider_DialsConfiguredEndpoint(t *testing.T) {
	// given a listener on a random localhost port + the connect signal channel
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	defer func() { _ = listener.Close() }()

	connected := make(chan struct{}, 1)
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			select {
			case connected <- struct{}{}:
			default:
			}
			_ = conn.Close()
		}
	}()

	// otlptracegrpc accepts an "http://host:port" URL and treats it as
	// insecure (no TLS), which is what NormalizeEndpoint already decodes.
	cfg := observability.Config{
		ServiceName: "runops-gateway",
		Endpoint:    "http://" + listener.Addr().String(),
	}

	// when
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tp, err := observability.SetupTracerProvider(ctx, cfg)
	if err != nil {
		t.Fatalf("SetupTracerProvider: %v", err)
	}
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer shutdownCancel()
		_ = tp.Shutdown(shutdownCtx)
	}()

	// emit one span and force-flush so the BatchSpanProcessor pushes to the
	// exporter immediately instead of waiting for its default 5s tick.
	tracer := tp.Tracer("test")
	_, span := tracer.Start(ctx, "test-span")
	span.End()
	flushCtx, flushCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer flushCancel()
	_ = tp.ForceFlush(flushCtx)

	// then a TCP dial to our listener arrives within the deadline
	select {
	case <-connected:
		// success
	case <-time.After(3 * time.Second):
		t.Fatalf("OTLP exporter never dialled the configured endpoint %s", listener.Addr())
	}
}

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

// TestBuildSampler_RespectsOTelSamplerEnv covers the trace-sampling decision.
// Production env (Cloud Run / VM systemd) sets OTEL_TRACES_SAMPLER and
// OTEL_TRACES_SAMPLER_ARG; the implementation must honor them, otherwise
// every span flows to Cloud Trace and quotas blow.
//
// Subtests cover the three values that matter:
//   - empty            -> ParentBased(AlwaysSample())   (dev default)
//   - parentbased_always_on
//   - parentbased_traceidratio + arg
//
// We assert on Description() rather than reaching into the unexported
// internals because Description is the documented stable surface.
func TestBuildSampler_RespectsOTelSamplerEnv(t *testing.T) {
	cases := []struct {
		name        string
		samplerName string
		samplerArg  string
		wantDesc    string
	}{
		{
			name:        "empty defaults to ParentBased(AlwaysOn)",
			samplerName: "",
			samplerArg:  "",
			wantDesc:    "ParentBased{root:AlwaysOnSampler",
		},
		{
			name:        "parentbased_always_on explicit",
			samplerName: "parentbased_always_on",
			samplerArg:  "",
			wantDesc:    "ParentBased{root:AlwaysOnSampler",
		},
		{
			name:        "parentbased_traceidratio at 0.1",
			samplerName: "parentbased_traceidratio",
			samplerArg:  "0.1",
			wantDesc:    "ParentBased{root:TraceIDRatioBased{0.1}",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := observability.Config{
				Sampler:    tc.samplerName,
				SamplerArg: tc.samplerArg,
			}
			s := observability.BuildSampler(cfg)
			if s == nil {
				t.Fatalf("BuildSampler returned nil")
			}
			if got := s.Description(); !strings.Contains(got, tc.wantDesc) {
				t.Errorf("sampler description = %q, want substring %q", got, tc.wantDesc)
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
