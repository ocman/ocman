package relay

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/usageforward"
	"github.com/ocman/ocman/internal/usagewire"
	collectorlog "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	"google.golang.org/protobuf/proto"
)

func TestDailyUsageRelayExportsOTLP(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "http/protobuf")
	t.Setenv("OTEL_EXPORTER_OTLP_LOGS_PROTOCOL", "http/protobuf")
	got := make(chan *collectorlog.ExportLogsServiceRequest, 1)
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload, err := io.ReadAll(r.Body)
		var req collectorlog.ExportLogsServiceRequest
		if err != nil || r.URL.Path != "/v1/logs" || proto.Unmarshal(payload, &req) != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		got <- &req
		w.Header().Set("Content-Type", "application/x-protobuf")
	}))
	defer collector.Close()
	forwarder, err := usageforward.New(t.Context(), collector.URL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = forwarder.Shutdown(t.Context()) })
	h := newHarness(t, func(cfg *Config) { cfg.Usage = forwarder })
	w := h.do(http.MethodPost, "/v1/usage/daily", []byte(`{"installationId":"3f70f326-5593-4b78-a31f-c67a23920334","date":"2026-08-12","version":"0.275.0","daily":{"sessions":1,"models":[{"model":"gpt-5.4","sessions":1,"inputTokens":10,"outputTokens":4}]}}`), "")
	if w.Code != http.StatusNoContent {
		t.Fatalf("status %d", w.Code)
	}
	records := (<-got).GetResourceLogs()[0].GetScopeLogs()[0].GetLogRecords()
	if len(records) != 3 || records[0].GetEventName() != "ocman.usage.daily" || records[1].GetEventName() != "ocman.usage.model.daily" || records[2].GetEventName() != "ocman.usage.security" {
		t.Fatal("missing daily or security events")
	}
}

func TestDailyUsageEndpoint(t *testing.T) {
	const payload = `{"installationId":"3f70f326-5593-4b78-a31f-c67a23920334","date":"2026-08-12","version":"0.275.0","daily":{"sessions":2,"models":[{"model":"gpt-5.4","sessions":2,"inputTokens":10,"outputTokens":4},{"model":"other","sessions":1,"inputTokens":8,"outputTokens":2}]}}`
	for _, tc := range []struct {
		path, body string
		want       int
	}{
		{"/v1/usage/daily", payload, 204},
		{"/v1/usage", payload, 400},
		{"/v1/usage/daily", strings.Replace(payload, "gpt-5.4", "private-model", 1), 400},
		{"/v1/usage/daily", strings.Replace(payload, "gpt-5.4", "other", 1), 400},
		{"/v1/usage/daily", strings.Replace(payload, `"inputTokens":10`, `"inputTokens":-10`, 1), 400},
		{"/v1/usage/daily", strings.Replace(payload, `"inputTokens":10`, `"inputTokens":10,"sessionId":"private"`, 1), 400},
		{"/v1/usage/daily", strings.Replace(payload, "2026-08-12", "2026-08-13", 1), 400},
		{"/v1/usage/daily", strings.Repeat(" ", 4097) + payload, 413},
	} {
		sink := &usageSink{}
		h := newHarness(t, func(cfg *Config) { cfg.Usage = sink })
		w := h.do(http.MethodPost, tc.path, []byte(tc.body), "")
		if w.Code != tc.want {
			t.Fatalf("status %d, want %d", w.Code, tc.want)
		}
		if tc.want == 204 {
			if len(sink.reports) != 1 || sink.reports[0].Daily.Models[0].InputTokens != 10 {
				t.Fatal("summary not forwarded")
			}
		} else if len(sink.reports) != 0 || len(sink.statuses) != 1 || sink.statuses[0] != tc.want {
			t.Fatal("rejection must only emit security event")
		}
	}
}

type usageSink struct {
	reports  []usagewire.Report
	ips      []string
	statuses []int
	err      error
}

func TestUsageRejectsInvalidPayloadsWithoutForwardingContent(t *testing.T) {
	for _, body := range []string{`{"prompt":"private"}`, `{} {}`, `null`, `{"installationId":"account","date":"2026-08-13","version":"0.275.0"}`, strings.Repeat(" ", 1025) + `{}`} {
		sink := &usageSink{}
		h := newHarness(t, func(cfg *Config) { cfg.Usage = sink })
		w := h.do("POST", "/v1/usage", []byte(body), "")
		if w.Code != 400 && w.Code != 413 {
			t.Fatal(w.Code, w.Body.String())
		}
		if len(sink.reports) != 0 || len(sink.statuses) != 1 || sink.statuses[0] != w.Code {
			t.Fatalf("unexpected export: %+v", sink)
		}
		if strings.Contains(w.Body.String(), "private") {
			t.Fatal("reflected payload")
		}
	}
}

func TestUsageOutageAndRateLimit(t *testing.T) {
	h := newHarness(t, nil)
	if w := h.do("POST", "/v1/usage", []byte(`{}`), ""); w.Code != 503 {
		t.Fatal(w.Code)
	}
	sink := &usageSink{err: errors.New("OTLP down")}
	h = newHarness(t, func(cfg *Config) { cfg.Usage = sink })
	body := []byte(`{"installationId":"3f70f326-5593-4b78-a31f-c67a23920334","date":"2026-08-13","version":"0.275.0"}`)
	if w := h.do("POST", "/v1/usage", body, ""); w.Code != 503 {
		t.Fatal(w.Code)
	}
	sink.err = nil
	for range 9 {
		if w := h.do("POST", "/v1/usage", body, ""); w.Code != 204 {
			t.Fatal(w.Code)
		}
	}
	if w := h.do("POST", "/v1/usage", body, ""); w.Code != 429 || w.Header().Get("Retry-After") == "" {
		t.Fatal(w.Code)
	}
	// A forged forwarded address cannot bypass limits unless explicitly trusted.
	r := httptest.NewRequest(http.MethodPost, "/v1/usage", strings.NewReader(string(body)))
	r.RemoteAddr = "192.0.2.1:9999"
	r.Header.Set("X-Forwarded-For", "203.0.113.4")
	w := httptest.NewRecorder()
	h.srv.ServeHTTP(w, r)
	if w.Code != 429 {
		t.Fatal(w.Code)
	}
}

func (s *usageSink) Forward(_ context.Context, report *usagewire.Report, ip string, status int) error {
	if report != nil {
		s.reports = append(s.reports, *report)
	}
	s.ips = append(s.ips, ip)
	s.statuses = append(s.statuses, status)
	return s.err
}

func TestUsageCollectionEndpoint(t *testing.T) {
	sink := &usageSink{}
	h := newHarness(t, func(cfg *Config) { cfg.Usage = sink })
	body := []byte(`{"installationId":"3f70f326-5593-4b78-a31f-c67a23920334","date":"2026-08-13","version":"0.275.0"}`)
	for range 2 {
		w := h.do(http.MethodPost, "/v1/usage", body, "")
		if w.Code != http.StatusNoContent {
			t.Fatalf("usage status = %d: %s", w.Code, w.Body.String())
		}
	}
	objects, err := h.store.List(t.Context(), "")
	if err != nil || len(objects) != 0 {
		t.Fatalf("relay must not store usage or IPs: %v, %v", objects, err)
	}
	if len(sink.reports) != 2 || sink.reports[0].Version != "0.275.0" || sink.ips[0] != "192.0.2.1" {
		t.Fatalf("forwarded: %+v", sink)
	}
	if w := h.do(http.MethodGet, "/v1/usage", nil, ""); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("no relay analytics endpoint: %d", w.Code)
	}
}
