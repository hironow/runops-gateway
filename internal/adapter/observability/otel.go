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
	"crypto/tls"
	"fmt"
	"strconv"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/oauth"
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

	// Sampler picks the OTel Sampler. Mirrors the standard env knob
	// OTEL_TRACES_SAMPLER. Currently supported values:
	//   ""                          -> ParentBased(AlwaysSample())  (dev / unset)
	//   "parentbased_always_on"     -> ParentBased(AlwaysSample())
	//   "parentbased_traceidratio"  -> ParentBased(TraceIDRatioBased(SamplerArg))
	// Anything else falls back to ParentBased(AlwaysSample()) and logs the
	// unknown value at the call site (the caller decides what to log).
	Sampler string

	// SamplerArg is the numeric argument for ratio-based samplers. Mirrors
	// OTEL_TRACES_SAMPLER_ARG. Parsed as float64; values outside [0,1] are
	// clamped by the OTel SDK.
	SamplerArg string

	// GCPProjectID is the GCP project the runtime SA belongs to. When set,
	// Cloud Trace's OTLP API (telemetry.googleapis.com) requires the
	// resource to carry an attribute literally named "gcp.project_id"
	// (NOT semconv cloud.account.id) — exporting without it returns
	// `InvalidArgument: Resource is missing required attribute "gcp.project_id"`.
	// Caller typically sets this from os.Getenv("GOOGLE_CLOUD_PROJECT");
	// empty value skips the attribute (local Jaeger doesn't need it).
	GCPProjectID string
}

// serviceNamespace is the org-level grouping every binary in this repo
// reports. Hard-coded because there is one organization-style answer.
const serviceNamespace = "runops"

// BuildResource composes the OTel Resource that every span produced by this
// binary should carry. service.namespace is hard-coded; service.name and
// service.version come from cfg. The GCP resource detector is intentionally
// not wired here yet — it lives behind its own helper so unit tests do not
// hit the GCP metadata server.
//
// gcp.project_id is appended when cfg.GCPProjectID is non-empty so the Cloud
// Trace OTLP endpoint accepts the export. We attach it as a plain string
// attribute (no semconv constant) because Cloud Trace's required key is
// literally "gcp.project_id", not the semconv equivalent cloud.account.id.
func BuildResource(ctx context.Context, cfg Config) (*resource.Resource, error) {
	attrs := []attribute.KeyValue{
		semconv.ServiceName(cfg.ServiceName),
		semconv.ServiceNamespace(serviceNamespace),
		semconv.ServiceVersion(cfg.ServiceVersion),
	}
	if cfg.GCPProjectID != "" {
		attrs = append(attrs, attribute.String("gcp.project_id", cfg.GCPProjectID))
	}
	return resource.New(ctx, resource.WithAttributes(attrs...))
}

// SetupTracerProvider returns a TracerProvider that the caller is responsible
// for shutting down (defer tp.Shutdown(ctx)). With Endpoint empty, the
// returned provider has no exporter wired up — Shutdown still succeeds, all
// spans are silently dropped. With Endpoint set, an OTLP gRPC exporter is
// built and attached via a BatchSpanProcessor.
//
// When the endpoint is secure (no http:// scheme), per-RPC ADC credentials
// are attached so the OTLP target (production: telemetry.googleapis.com)
// authenticates the runtime SA. Local Jaeger over insecure gRPC skips this.
func SetupTracerProvider(ctx context.Context, cfg Config) (*sdktrace.TracerProvider, error) {
	res, err := BuildResource(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("observability: build resource: %w", err)
	}
	sampler := BuildSampler(cfg)

	endpoint, insecure, ok := NormalizeEndpoint(cfg.Endpoint)
	if !ok {
		return sdktrace.NewTracerProvider(
			sdktrace.WithResource(res),
			sdktrace.WithSampler(sampler),
		), nil
	}

	opts := []otlptracegrpc.Option{otlptracegrpc.WithEndpoint(endpoint)}
	if insecure {
		opts = append(opts, otlptracegrpc.WithInsecure())
	} else {
		// Per ADR 0020 the production endpoint is telemetry.googleapis.com:443
		// which fronts the Cloud Trace OTLP API. ADC + per-RPC OAuth tokens
		// are how the SDK authenticates the runtime service account.
		creds, credsErr := oauth.NewApplicationDefault(ctx,
			"https://www.googleapis.com/auth/trace.append")
		if credsErr != nil {
			return nil, fmt.Errorf("observability: ADC for OTLP: %w", credsErr)
		}
		opts = append(opts,
			otlptracegrpc.WithTLSCredentials(credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12})),
			otlptracegrpc.WithDialOption(grpc.WithPerRPCCredentials(creds)),
		)
	}
	exporter, err := otlptrace.New(ctx, otlptracegrpc.NewClient(opts...))
	if err != nil {
		return nil, fmt.Errorf("observability: build OTLP gRPC exporter: %w", err)
	}

	return sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sampler),
		sdktrace.WithBatcher(exporter),
	), nil
}

// BuildSampler turns OTEL_TRACES_SAMPLER (cfg.Sampler) +
// OTEL_TRACES_SAMPLER_ARG (cfg.SamplerArg) into an sdktrace.Sampler. Unknown
// or empty values fall back to ParentBased(AlwaysSample()) — the dev-friendly
// default. Production deploys set parentbased_traceidratio + an arg.
func BuildSampler(cfg Config) sdktrace.Sampler {
	switch cfg.Sampler {
	case "", "parentbased_always_on":
		return sdktrace.ParentBased(sdktrace.AlwaysSample())
	case "parentbased_traceidratio":
		ratio, err := strconv.ParseFloat(cfg.SamplerArg, 64)
		if err != nil {
			ratio = 1.0 // mis-typed env: be safe and sample everything
		}
		return sdktrace.ParentBased(sdktrace.TraceIDRatioBased(ratio))
	default:
		return sdktrace.ParentBased(sdktrace.AlwaysSample())
	}
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
