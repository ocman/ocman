package telemetry

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"

	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

// target holds the parsed --otel endpoint. ocman exports OTLP/HTTP only.
type target struct {
	endpoint string // "host:port" with no scheme; what the OTLP exporters expect.
	urlPath  string // the path component (kept for non-default collectors).
	insecure bool   // true when the URL scheme is plaintext.
	// fromGRPC is set when a gRPC-style endpoint was rewritten to OTLP/HTTP.
	fromGRPC bool
}

// String renders the effective OTLP/HTTP URL.
func (t target) String() string {
	scheme := "https"
	if t.insecure {
		scheme = "http"
	}
	return scheme + "://" + t.endpoint + t.urlPath
}

// parseEndpoint turns the --otel value into an OTLP/HTTP target:
//
//   - http://, https://       -> OTLP/HTTP (TLS implied by https://)
//   - grpc://, grpcs://       -> rewritten to http:// / https://
//   - bare host[:port]        -> rewritten to http://
//
// OTLP/gRPC is not supported. Endpoints that asked for it are rewritten
// with fromGRPC set so Init can warn, and the conventional gRPC port 4317
// becomes the OTLP/HTTP port 4318.
func parseEndpoint(raw string) (target, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return target{}, fmt.Errorf("empty endpoint")
	}
	if !strings.Contains(raw, "://") {
		return grpcFallback(raw, true), nil
	}

	u, err := url.Parse(raw)
	if err != nil {
		return target{}, err
	}
	if u.Host == "" {
		return target{}, fmt.Errorf("URL %q has no host", raw)
	}

	switch strings.ToLower(u.Scheme) {
	case "http":
		return target{endpoint: u.Host, urlPath: u.Path, insecure: true}, nil
	case "https":
		return target{endpoint: u.Host, urlPath: u.Path}, nil
	case "grpc":
		return grpcFallback(u.Host, true), nil
	case "grpcs":
		return grpcFallback(u.Host, false), nil
	default:
		return target{}, fmt.Errorf("unsupported scheme %q (want http|https)", u.Scheme)
	}
}

func grpcFallback(host string, insecure bool) target {
	if h, port, err := net.SplitHostPort(host); err == nil && port == "4317" {
		host = net.JoinHostPort(h, "4318")
	}
	return target{endpoint: host, insecure: insecure, fromGRPC: true}
}

// customPath reports whether the target overrides the per-signal default
// paths (/v1/traces, /v1/metrics, /v1/logs).
func (t target) customPath() bool { return t.urlPath != "" && t.urlPath != "/" }

func newTraceExporter(ctx context.Context, t target) (*otlptrace.Exporter, error) {
	opts := []otlptracehttp.Option{otlptracehttp.WithEndpoint(t.endpoint)}
	if t.insecure {
		opts = append(opts, otlptracehttp.WithInsecure())
	}
	if t.customPath() {
		opts = append(opts, otlptracehttp.WithURLPath(t.urlPath))
	}
	return otlptracehttp.New(ctx, opts...)
}

func newMetricExporter(ctx context.Context, t target) (sdkmetric.Exporter, error) {
	opts := []otlpmetrichttp.Option{otlpmetrichttp.WithEndpoint(t.endpoint)}
	if t.insecure {
		opts = append(opts, otlpmetrichttp.WithInsecure())
	}
	if t.customPath() {
		opts = append(opts, otlpmetrichttp.WithURLPath(t.urlPath))
	}
	return otlpmetrichttp.New(ctx, opts...)
}

func newLogExporter(ctx context.Context, t target) (sdklog.Exporter, error) {
	opts := []otlploghttp.Option{otlploghttp.WithEndpoint(t.endpoint)}
	if t.insecure {
		opts = append(opts, otlploghttp.WithInsecure())
	}
	if t.customPath() {
		opts = append(opts, otlploghttp.WithURLPath(t.urlPath))
	}
	return otlploghttp.New(ctx, opts...)
}
