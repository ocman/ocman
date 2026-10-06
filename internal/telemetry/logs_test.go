package telemetry

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	log "github.com/sirupsen/logrus"
	otlplogrus "go.opentelemetry.io/contrib/bridges/otellogrus"
	"go.opentelemetry.io/otel"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	collectorlog "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	"google.golang.org/grpc"
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

type logCollector struct {
	collectorlog.UnimplementedLogsServiceServer
	received chan *collectorlog.ExportLogsServiceRequest
}

func (c *logCollector) Export(_ context.Context, req *collectorlog.ExportLogsServiceRequest) (*collectorlog.ExportLogsServiceResponse, error) {
	c.received <- req
	return &collectorlog.ExportLogsServiceResponse{}, nil
}

func TestLogExporterGRPC(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	collector := &logCollector{received: make(chan *collectorlog.ExportLogsServiceRequest, 1)}
	collectorlog.RegisterLogsServiceServer(server, collector)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	exp, err := newLogExporter(t.Context(), target{protocol: protoGRPC, endpoint: listener.Addr().String(), insecure: true})
	if err != nil {
		t.Fatal(err)
	}
	provider := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewBatchProcessor(exp)))
	logger := log.New()
	logger.SetOutput(io.Discard)
	logger.AddHook(otlplogrus.NewHook("test", otlplogrus.WithLoggerProvider(provider)))
	logger.Info("grpc log")
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := provider.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case req := <-collector.received:
		if got := req.ResourceLogs[0].ScopeLogs[0].LogRecords[0].Body.GetStringValue(); got != "grpc log" {
			t.Fatalf("unexpected log: %q", got)
		}
	default:
		t.Fatal("gRPC exporter did not deliver log")
	}
}

func TestLogExporterTLSAndUnknownProtocol(t *testing.T) {
	for _, transport := range []protocol{protoHTTP, protoGRPC} {
		exp, err := newLogExporter(t.Context(), target{protocol: transport, endpoint: "localhost:4317"})
		if err != nil {
			t.Fatal(err)
		}
		if err := exp.Shutdown(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := newLogExporter(t.Context(), target{}); err == nil {
		t.Fatal("log exporter accepted an unknown protocol")
	}
}
