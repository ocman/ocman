// Package usageforward forwards validated usage events to OTLP/HTTP without
// local storage or a background queue. Installation IDs are log attributes,
// never metric labels or resource attributes.
package usageforward

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"

	"github.com/ocman/ocman/internal/usagewire"
)

type Forwarder struct {
	exporter sdklog.Exporter
	provider *sdklog.LoggerProvider
	logger   otellog.Logger
	now      func() time.Time
}

// New returns nil when no OTLP destination is configured. The relay then rejects
// reports with 503 instead of acknowledging and silently losing them.
// Standard OTLP headers, including per-signal headers, are read by the exporter.
func New(ctx context.Context, baseURL string) (*Forwarder, error) {
	endpoint, err := endpointURL(baseURL)
	if err != nil || endpoint == "" {
		return nil, err
	}
	exporter, err := otlploghttp.New(ctx,
		otlploghttp.WithEndpointURL(endpoint),
		otlploghttp.WithRetry(otlploghttp.RetryConfig{Enabled: false}),
		otlploghttp.WithHTTPClient(&http.Client{Timeout: 3 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}))
	if err != nil {
		return nil, err
	}
	return newForwarder(exporter), nil
}

func endpointURL(base string) (string, error) {
	appendPath := true
	if base == "" {
		base = os.Getenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT")
		appendPath = base == ""
		if base == "" {
			base = os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
		}
	}
	if base == "" {
		return "", nil
	}
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", errors.New("usage OTLP endpoint must be an http(s) URL without credentials, query or fragment")
	}
	if protocol := os.Getenv("OTEL_EXPORTER_OTLP_LOGS_PROTOCOL"); protocol != "" && protocol != "http/protobuf" {
		return "", errors.New("usage forwarding requires OTLP/HTTP protobuf")
	} else if protocol == "" {
		if protocol = os.Getenv("OTEL_EXPORTER_OTLP_PROTOCOL"); protocol != "" && protocol != "http/protobuf" {
			return "", errors.New("usage forwarding requires OTLP/HTTP protobuf")
		}
	}
	if appendPath {
		u.Path = strings.TrimRight(u.Path, "/") + "/v1/logs"
	}
	return u.String(), nil
}

func newForwarder(exporter sdklog.Exporter) *Forwarder {
	provider := sdklog.NewLoggerProvider(
		sdklog.WithResource(resource.NewSchemaless(attribute.String("service.name", "ocman-relay"))),
		sdklog.WithProcessor(captureProcessor{}))
	return &Forwarder{exporter: exporter, provider: provider,
		logger: provider.Logger("github.com/ocman/ocman/usage"), now: time.Now}
}

// Forward exports a usage event and a separate IP security event in one bounded
// request. Retries may duplicate events; the OTEL backend must count distinct
// installation IDs, not event totals. No export error logs contain payloads.
func (f *Forwarder) Forward(ctx context.Context, report *usagewire.Report, ip string, status int) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var records []sdklog.Record
	ctx = context.WithValue(ctx, recordsKey{}, &records)
	now := f.now()
	if report != nil {
		day, err := time.Parse(time.DateOnly, report.Date)
		if err != nil {
			return err
		}
		f.emit(ctx, "ocman.usage.active", day, now,
			attribute.String("ocman.installation.id", report.InstallationID),
			attribute.String("ocman.activity.date", report.Date),
			attribute.String("ocman.version", report.Version))
	}
	f.emit(ctx, "ocman.usage.security", now, now,
		attribute.String("client.address", ip), attribute.Int("http.response.status_code", status))
	return f.exporter.Export(ctx, records)
}

func (f *Forwarder) emit(ctx context.Context, name string, at, observed time.Time, attrs ...attribute.KeyValue) {
	var record otellog.Record
	record.SetEventName(name)
	record.SetBody(attribute.StringValue(name))
	record.SetTimestamp(at)
	record.SetObservedTimestamp(observed)
	record.SetSeverity(otellog.SeverityInfo)
	record.AddAttributes(attrs...)
	f.logger.Emit(ctx, record)
}

func (f *Forwarder) Shutdown(ctx context.Context) error {
	return errors.Join(f.provider.Shutdown(ctx), f.exporter.Shutdown(ctx))
}
