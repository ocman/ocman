package usageforward

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/usagewire"
	"go.opentelemetry.io/otel/attribute"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	collectorlog "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	"google.golang.org/protobuf/proto"
)

type memoryExporter struct {
	mu      sync.Mutex
	records []sdklog.Record
	err     error
}

func (e *memoryExporter) Export(_ context.Context, records []sdklog.Record) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.records = append(e.records, records...)
	return e.err
}
func (*memoryExporter) Shutdown(context.Context) error   { return nil }
func (*memoryExporter) ForceFlush(context.Context) error { return nil }

func attributes(record *sdklog.Record) map[string]any {
	attrs := map[string]any{}
	record.WalkAttributes(func(kv attribute.KeyValue) bool { attrs[string(kv.Key)] = kv.Value.AsInterface(); return true })
	return attrs
}

func TestSeparateUsageAndSecurityEvents(t *testing.T) {
	exporter := &memoryExporter{}
	f := newForwarder(exporter)
	t.Cleanup(func() { _ = f.Shutdown(t.Context()) })
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	f.now = func() time.Time { return now }
	report := &usagewire.Report{InstallationID: "3f70f326-5593-4b78-a31f-c67a23920334", Date: "2026-11-11", Version: "0.275.0"}
	if err := f.Forward(t.Context(), report, "192.0.2.1", 204); err != nil {
		t.Fatal(err)
	}
	if len(exporter.records) != 2 {
		t.Fatal(len(exporter.records))
	}
	usage, security := exporter.records[0], exporter.records[1]
	ua, sa := attributes(&usage), attributes(&security)
	if usage.EventName() != "ocman.usage.active" || len(ua) != 3 || ua["ocman.installation.id"] != report.InstallationID || ua["client.address"] != nil {
		t.Fatal(usage.EventName(), ua)
	}
	if security.EventName() != "ocman.usage.security" || len(sa) != 2 || sa["client.address"] != "192.0.2.1" || sa["ocman.installation.id"] != nil {
		t.Fatal(security.EventName(), sa)
	}
	if usage.Timestamp().Hour() != 0 || !usage.ObservedTimestamp().Equal(now) {
		t.Fatal("usage event must retain only the UTC day")
	}
	if value, ok := usage.Resource().Set().Value(attribute.Key("service.name")); !ok || value.AsString() != "ocman-relay" {
		t.Fatal(usage.Resource())
	}
	exporter.err = errors.New("offline")
	if err := f.Forward(t.Context(), nil, "192.0.2.1", 400); err == nil {
		t.Fatal("export error must reach caller")
	}
	if len(exporter.records) != 3 {
		t.Fatal("rejected reports must export security only")
	}
	report.Date = "invalid"
	if err := f.Forward(t.Context(), report, "192.0.2.1", 400); err == nil {
		t.Fatal("invalid date accepted")
	}
}

func clearEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{"OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", "OTEL_EXPORTER_OTLP_PROTOCOL", "OTEL_EXPORTER_OTLP_LOGS_PROTOCOL", "OTEL_EXPORTER_OTLP_HEADERS", "OTEL_EXPORTER_OTLP_LOGS_HEADERS", "OTEL_EXPORTER_OTLP_COMPRESSION"} {
		t.Setenv(name, "")
	}
}

func TestOTLPHTTPDeliveryAndFailure(t *testing.T) {
	clearEnv(t)
	var received collectorlog.ExportLogsServiceRequest
	status, calls := 200, 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/logs" || r.Header.Get("Authorization") != "Bearer test" {
			t.Errorf("request path=%s headers=%v", r.URL.Path, r.Header)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		if err := proto.Unmarshal(body, &received); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(status)
	}))
	defer ts.Close()
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", ts.URL)
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "Authorization=Bearer%20test")
	f, err := New(t.Context(), "")
	if err != nil || f == nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Shutdown(t.Context()) }()
	if err := f.Forward(t.Context(), nil, "192.0.2.1", 400); err != nil {
		t.Fatal(err)
	}
	if len(received.ResourceLogs) != 1 || len(received.ResourceLogs[0].ScopeLogs[0].LogRecords) != 1 {
		t.Fatal(&received)
	}
	status = 503
	if err := f.Forward(t.Context(), nil, "192.0.2.1", 400); err == nil {
		t.Fatal("OTLP outage acknowledged")
	}
	if calls != 2 {
		t.Fatalf("unexpected hidden exporter retries: %d", calls)
	}
}

func TestEndpointConfiguration(t *testing.T) {
	clearEnv(t)
	if f, err := New(t.Context(), ""); f != nil || err != nil {
		t.Fatal(f, err)
	}
	for _, bad := range []string{"ftp://host", "https://user:secret@host", "https://host?secret=1", "https://host#fragment", "://"} {
		if _, err := New(t.Context(), bad); err == nil {
			t.Fatal("accepted", bad)
		}
	}
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://collector:4318/base")
	if got, err := endpointURL(""); err != nil || got != "http://collector:4318/base/v1/logs" {
		t.Fatal(got, err)
	}
	t.Setenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", "https://logs.example.com/custom")
	if got, err := endpointURL(""); err != nil || got != "https://logs.example.com/custom" {
		t.Fatal(got, err)
	}
	if got, err := endpointURL("https://flag.example.com/"); err != nil || got != "https://flag.example.com/v1/logs" {
		t.Fatal(got, err)
	}
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "grpc")
	if _, err := endpointURL(""); err == nil {
		t.Fatal("grpc accepted")
	}
	t.Setenv("OTEL_EXPORTER_OTLP_LOGS_PROTOCOL", "http/protobuf")
	if _, err := endpointURL(""); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OTEL_EXPORTER_OTLP_LOGS_PROTOCOL", "invalid")
	if _, err := endpointURL(""); err == nil {
		t.Fatal("invalid protocol accepted")
	}
}
