package analytics

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

var conversationAnalyticsRoutes = []string{
	"/api/stats", "/api/activity", "/api/models", "/api/hourly", "/api/hourly-tokens",
	"/api/analytics/overview", "/api/analytics/session-concurrency", "/api/analytics/agent-run-hours",
	"/api/metrics", "/api/metrics/performance", "/api/metric-logs?kind=request",
	"/api/metric-logs?kind=session", "/api/metric-logs?kind=project",
}

func TestAllConversationAnalyticsUseStatev2WithFiltersAndPagination(t *testing.T) {
	f, h, now := analyticsParityFixture(t)
	h.CheckedAt = func() time.Time { return now }
	for _, route := range conversationAnalyticsRoutes {
		for _, filter := range []string{"", "dir=/repo", "model=p%2Forg%2Fmodel&agent=build", "limit=1&offset=1&sessionLimit=1&sessionOffset=1&projectLimit=1&projectOffset=1"} {
			t.Run(route+filter, func(t *testing.T) {
				separator := "?"
				if strings.Contains(route, "?") {
					separator = "&"
				}
				// Explicit since keeps the concurrency bucket origin identical.
				target := route + separator + "since=" + strconv.FormatInt(now.Add(-48*time.Hour).UnixMilli(), 10) + "&" + filter
				rec := f.serve(http.MethodGet, target, "")
				var got any
				decode(t, rec, &got)
				if rec.Header().Get("X-Ocman-Analytics-Source") != "statev2" {
					t.Fatalf("did not cut over: %v", rec.Header())
				}
				if rec.Header().Get("X-Ocman-Analytics-Checked-At") == "" {
					t.Fatal("missing freshness")
				}
			})
		}
	}
	// Closing the original source must not break a selected history reader.
	f.legacy.Close()
	f.legacy = nil
	for _, route := range conversationAnalyticsRoutes {
		rec := f.serve(http.MethodGet, route, "")
		if rec.Code != http.StatusOK || rec.Header().Get("X-Ocman-Analytics-Source") != "statev2" {
			t.Fatalf("%s still used legacy: %d %s %v", route, rec.Code, rec.Body, rec.Header())
		}
	}
}

func TestAnalyticsUnavailableAndQueryErrorsNeverUseLegacy(t *testing.T) {
	f, h, _ := analyticsParityFixture(t)
	h.Instances = func() ([]string, bool) { return nil, false }
	rec := f.serve(http.MethodGet, "/api/models", "")
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("X-Ocman-Analytics-Error") != "import-incomplete" {
		t.Fatalf("incomplete history silently used legacy: %d %v", rec.Code, rec.Header())
	}
	h.Instances = func() ([]string, bool) { return []string{"one"}, true }
	// Force only the selected query to fail, not scope validation or legacy.
	calls := 0
	handler := f.analyticsRoute(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if f.analyticsReader(r.Context()) != f.legacy {
			http.Error(w, "projection failed", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"legacy":true}`))
	})
	w := httptest.NewRecorder()
	handler(w, httptest.NewRequest(http.MethodGet, "/api/models", nil))
	if w.Code != http.StatusInternalServerError || calls != 1 || !strings.Contains(w.Body.String(), "projection failed") || w.Header().Get("X-Ocman-Analytics-Source") != "statev2" {
		t.Fatalf("query error retried legacy: %d %s %v calls=%d", w.Code, w.Body, w.Header(), calls)
	}
	f.Finalize()
}

func TestUnrelatedAnalyticsKeepTheirSources(t *testing.T) {
	f, _, _ := analyticsParityFixture(t)
	for _, route := range []string{"/api/permission-stats", "/api/analytics/ui-usage", "/api/analytics/database-sizes"} {
		rec := f.serve(http.MethodGet, route, "")
		if rec.Code != http.StatusOK || rec.Header().Get("X-Ocman-Analytics-Source") != "" {
			t.Fatalf("%s incorrectly cut over: %d %v", route, rec.Code, rec.Header())
		}
	}
	// Confirm the response encoder remains untouched by middleware.
	var ignored any
	if err := json.Unmarshal(f.serve(http.MethodGet, "/api/models", "").Body.Bytes(), &ignored); err != nil {
		t.Fatal(err)
	}
}

func TestAnalyticsStrictSourceErrorsAndMissingLegacyRuntime(t *testing.T) {
	f, h, _ := analyticsParityFixture(t)
	for _, tc := range []struct{ name, reason string }{
		{"no-store", "history-unavailable"}, {"not-ready", "import-incomplete"}, {"empty-scope", "import-incomplete"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, instances := h.Store, h.Instances
			switch tc.name {
			case "no-store":
				h.Store = nil
			case "not-ready":
				h.Instances = func() ([]string, bool) { return nil, false }
			case "empty-scope":
				h.Instances = func() ([]string, bool) { return nil, true }
			}
			rec := f.serve(http.MethodGet, "/api/models", "")
			wantCode := http.StatusServiceUnavailable
			if rec.Code != wantCode || rec.Header().Get("X-Ocman-Analytics-Error") != tc.reason {
				t.Fatalf("%d %v", rec.Code, rec.Header())
			}
			h.Store, h.Instances = store, instances
		})
	}
	// Claude-only analytics have no OpenCode runtime DB. The selected history
	// reader still supports concurrency and runtime queries.
	f.legacy = nil
	for _, route := range []string{"/api/analytics/session-concurrency", "/api/analytics/agent-run-hours"} {
		rec := f.serve(http.MethodGet, route, "")
		if rec.Code != 200 || rec.Header().Get("X-Ocman-Analytics-Source") != "statev2" {
			t.Fatalf("%s: %d %s", route, rec.Code, rec.Body)
		}
	}
	f.Finalize()
}

func TestEveryAnalyticsRouteRejectsIncompleteHistory(t *testing.T) {
	f, h, _ := analyticsParityFixture(t)
	h.Instances = func() ([]string, bool) { return nil, false }
	f.legacy.Close() // legacy must not be queried, even as a retry
	for _, route := range conversationAnalyticsRoutes {
		rec := f.serve(http.MethodGet, route, "")
		if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("X-Ocman-Analytics-Source") != "statev2" {
			t.Fatalf("%s: %d %s %v", route, rec.Code, rec.Body, rec.Header())
		}
		if rec.Header().Get("X-Ocman-Analytics-Fallback") != "" {
			t.Fatal("fallback diagnostics remain", rec.Header())
		}
	}
}
