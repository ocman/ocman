package relay

import (
	"context"
	"errors"
	"github.com/ocman/ocman/internal/usagewire"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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
