package telemetry

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

// protocol describes the OTLP transport selected from the endpoint URL.
type protocol string

const (
	protoHTTP protocol = "http"
	protoGRPC protocol = "grpc"
)

// target holds the parsed --otel endpoint after URL-scheme detection.
type target struct {
	protocol protocol
	endpoint string // "host:port" with no scheme; what the OTLP exporters expect.
	urlPath  string // for HTTP, the path component (kept for non-default collectors).
	insecure bool   // true when the URL scheme is plaintext.
}

// parseEndpoint inspects the URL scheme to decide between OTLP/HTTP
// and OTLP/gRPC. The OTel exporter constructors take the host:port
// part directly, not a full URL, so we strip the scheme after deciding.
//
// Defaults:
//   - http://, https://       -> OTLP/HTTP (TLS implied by https://)
//   - grpc://, grpcs://       -> OTLP/gRPC (TLS for grpcs://)
//   - bare host[:port]        -> OTLP/gRPC, insecure (matches the
//                                spec's default for OTLP/gRPC).
//
// The collector port hint isn't enforced — operators occasionally
// front a collector with their own ingress and use 443/80 instead
// of 4317/4318.
func parseEndpoint(raw string) (target, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return target{}, fmt.Errorf("empty endpoint")
	}

	if !strings.Contains(raw, "://") {
		// Bare host[:port] -> default to gRPC insecure, matching the
		// OTel spec's default OTLP transport.
		return target{
			protocol: protoGRPC,
			endpoint: raw,
			insecure: true,
		}, nil
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
		return target{protocol: protoHTTP, endpoint: u.Host, urlPath: u.Path, insecure: true}, nil
	case "https":
		return target{protocol: protoHTTP, endpoint: u.Host, urlPath: u.Path, insecure: false}, nil
	case "grpc":
		return target{protocol: protoGRPC, endpoint: u.Host, insecure: true}, nil
	case "grpcs":
		return target{protocol: protoGRPC, endpoint: u.Host, insecure: false}, nil
	default:
		return target{}, fmt.Errorf("unsupported scheme %q (want http|https|grpc|grpcs)", u.Scheme)
	}
}

// newTraceExporter builds the OTLP trace exporter for the given target.
func newTraceExporter(ctx context.Context, t target) (*otlptrace.Exporter, error) {
	switch t.protocol {
	case protoHTTP:
		opts := []otlptracehttp.Option{otlptracehttp.WithEndpoint(t.endpoint)}
		if t.insecure {
			opts = append(opts, otlptracehttp.WithInsecure())
		}
		if t.urlPath != "" && t.urlPath != "/" {
			opts = append(opts, otlptracehttp.WithURLPath(t.urlPath))
		}
		return otlptracehttp.New(ctx, opts...)
	case protoGRPC:
		opts := []otlptracegrpc.Option{otlptracegrpc.WithEndpoint(t.endpoint)}
		if t.insecure {
			opts = append(opts, otlptracegrpc.WithInsecure())
		}
		return otlptracegrpc.New(ctx, opts...)
	default:
		return nil, fmt.Errorf("unknown protocol %q", t.protocol)
	}
}

// newMetricExporter builds the OTLP metric exporter for the given target.
func newMetricExporter(ctx context.Context, t target) (sdkmetric.Exporter, error) {
	switch t.protocol {
	case protoHTTP:
		opts := []otlpmetrichttp.Option{otlpmetrichttp.WithEndpoint(t.endpoint)}
		if t.insecure {
			opts = append(opts, otlpmetrichttp.WithInsecure())
		}
		if t.urlPath != "" && t.urlPath != "/" {
			opts = append(opts, otlpmetrichttp.WithURLPath(t.urlPath))
		}
		return otlpmetrichttp.New(ctx, opts...)
	case protoGRPC:
		opts := []otlpmetricgrpc.Option{otlpmetricgrpc.WithEndpoint(t.endpoint)}
		if t.insecure {
			opts = append(opts, otlpmetricgrpc.WithInsecure())
		}
		return otlpmetricgrpc.New(ctx, opts...)
	default:
		return nil, fmt.Errorf("unknown protocol %q", t.protocol)
	}
}
