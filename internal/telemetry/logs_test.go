package telemetry

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	log "github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel"
	collectorlog "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	"google.golang.org/protobuf/proto"
)

func TestInitExportsLogs(t *testing.T) {
	for _, path := range []string{"", "/custom"} {
		t.Run("path="+path, func(t *testing.T) {
			received := make(chan *collectorlog.ExportLogsServiceRequest, 10)
			collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				logPath := path
				if logPath == "" {
					logPath = "/v1/logs"
				}
				if r.URL.Path == logPath {
					body, err := io.ReadAll(r.Body)
					if err != nil {
						t.Error(err)
						return
					}
					var req collectorlog.ExportLogsServiceRequest
					// A custom path is shared by all signals; only decode logs.
					if err := proto.Unmarshal(body, &req); err == nil && len(req.ResourceLogs) > 0 {
						received <- &req
					}
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer collector.Close()
			logger := log.StandardLogger()
			oldHooks := logger.ReplaceHooks(make(log.LevelHooks))
			oldOutput, oldLevel := logger.Out, logger.GetLevel()
			var console bytes.Buffer
			logger.SetOutput(&console)
			logger.SetLevel(log.InfoLevel)
			t.Cleanup(func() {
				logger.ReplaceHooks(oldHooks)
				logger.SetOutput(oldOutput)
				logger.SetLevel(oldLevel)
			})
			t.Setenv("OTEL_SERVICE_NAME", "ocman-logs-test")
			shutdown, err := Init(t.Context(), collector.URL+path, "test")
			if err != nil {
				t.Fatal(err)
			}
			log.WithField("count", 42).Info("standalone log")
			ctx, span := otel.Tracer("test").Start(t.Context(), "logged request")
			traceID := span.SpanContext().TraceID()
			log.WithContext(ctx).Warn("correlated log")
			log.Debug("filtered log")
			span.End()
			flushCtx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			if err := shutdown(flushCtx); err != nil {
				t.Fatal(err)
			}
			select {
			case req := <-received:
				foundStandalone, foundCorrelated := false, false
				for _, resource := range req.ResourceLogs {
					foundService := false
					for _, attr := range resource.Resource.Attributes {
						if attr.Key == "service.name" && attr.Value.GetStringValue() == "ocman-logs-test" {
							foundService = true
						}
					}
					if !foundService {
						t.Fatal("log resource missing service identity")
					}
					for _, scope := range resource.ScopeLogs {
						for _, record := range scope.LogRecords {
							switch record.Body.GetStringValue() {
							case "standalone log":
								for _, attr := range record.Attributes {
									if attr.Key == "count" && attr.Value.GetIntValue() == 42 && record.SeverityText == "info" {
										foundStandalone = true
									}
								}
							case "correlated log":
								foundCorrelated = bytes.Equal(record.TraceId, traceID[:]) && record.SeverityText == "warning"
							case "filtered log":
								t.Fatal("exported a log below the configured level")
							}
						}
					}
				}
				if !foundStandalone || !foundCorrelated {
					t.Fatalf("missing expected logs: standalone=%v correlated=%v", foundStandalone, foundCorrelated)
				}
			default:
				t.Fatal("shutdown did not export logs")
			}
			if !strings.Contains(console.String(), "standalone log") || !strings.Contains(console.String(), "correlated log") {
				t.Fatal("OTLP hook suppressed console output")
			}
		})
	}
}

func TestLogExporterTLS(t *testing.T) {
	exp, err := newLogExporter(t.Context(), target{endpoint: "localhost:4318"})
	if err != nil {
		t.Fatal(err)
	}
	if err := exp.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
}
