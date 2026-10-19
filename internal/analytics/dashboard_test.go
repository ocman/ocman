package analytics

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/module"
	"github.com/NoUseFreak/ocman/internal/pricing"
)

func TestConversationAnalyticsRoutesRequireHistory(t *testing.T) {
	mux := routes(newTestModule(Deps{}))
	for _, path := range []string{
		"/api/stats", "/api/metrics", "/api/metrics/performance", "/api/metric-logs?kind=request",
		"/api/activity", "/api/models", "/api/hourly", "/api/hourly-tokens",
		"/api/analytics/overview", "/api/analytics/session-concurrency", "/api/analytics/agent-run-hours",
	} {
		if rec := serve(mux, http.MethodGet, path, ""); rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s: got %d, want 503: %s", path, rec.Code, rec.Body.String())
		}
	}
}

// The usage views pass model/dir/day filters straight through; assert
// they stay 200 with an empty database rather than pinning SQL shapes.
func TestUsageRoutesFilterParams(t *testing.T) {
	f := newFixture(t)
	queries := []string{
		"",
		"?days=0",
		"?days=7&model=anthropic/claude",
		"?since=1700000000000&dir=" + url.QueryEscape("/tmp/project"),
		"?limit=abc&offset=abc&sessionLimit=5&sessionOffset=1&projectLimit=5&projectOffset=1",
	}
	for _, path := range []string{"/api/activity", "/api/models", "/api/hourly", "/api/hourly-tokens", "/api/metrics"} {
		for _, q := range queries {
			if rec := f.serve(http.MethodGet, path+q, ""); rec.Code != http.StatusOK {
				t.Errorf("%s%s: got %d, want 200: %s", path, q, rec.Code, rec.Body.String())
			}
		}
	}
}

func TestEmptyDatabaseShapes(t *testing.T) {
	f := newFixture(t)
	f.deps.Version = func() string { return "v1.2.3-test" }
	var stats map[string]any
	decode(t, f.serve(http.MethodGet, "/api/stats", ""), &stats)
	if stats["totalSessions"].(float64) != 0 || stats["version"] != "v1.2.3-test" {
		t.Fatalf("stats = %v", stats)
	}
	var metrics map[string]any
	decode(t, f.serve(http.MethodGet, "/api/metrics?days=30", ""), &metrics)
	if _, ok := metrics["summary"]; !ok {
		t.Fatal("metrics payload has no summary")
	}
	var activity, hourly, models []any
	decode(t, f.serve(http.MethodGet, "/api/activity", ""), &activity)
	if len(activity) != 366 { // 365 days + today, no filter
		t.Errorf("activity = %d days, want 366", len(activity))
	}
	decode(t, f.serve(http.MethodGet, "/api/hourly", ""), &hourly)
	if len(hourly) != 24 {
		t.Errorf("hourly = %d hours, want 24", len(hourly))
	}
	decode(t, f.serve(http.MethodGet, "/api/models", ""), &models)
}

func decode(t *testing.T, rec *httptest.ResponseRecorder, dst any) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), dst); err != nil {
		t.Fatalf("invalid JSON: %v (%s)", err, rec.Body.String())
	}
}

func TestCalcCost(t *testing.T) {
	mux := routes(newTestModule(Deps{}))
	table := pricing.Load()
	tests := []struct {
		name     string
		body     string
		wantCode int
		want     float64
	}{
		{"malformed json", "{", http.StatusBadRequest, 0},
		{"empty body", "", http.StatusBadRequest, 0},
		{"wrong types", `{"input":"lots"}`, http.StatusBadRequest, 0},
		{"unknown model", `{"modelID":"nope/nope","input":10,"output":5}`, http.StatusOK, table.CalcCost("nope/nope", 10, 5, 0, 0)},
		{"zero usage", `{"modelID":"","input":0,"output":0}`, http.StatusOK, 0},
		{"with cache tokens", `{"modelID":"anthropic/claude-sonnet-4-20250514","input":100,"output":50,"cacheRead":10,"cacheWrite":20}`, http.StatusOK,
			table.CalcCost("anthropic/claude-sonnet-4-20250514", 100, 50, 10, 20)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := serve(mux, http.MethodPost, "/api/cost/calc", tt.body)
			if rec.Code != tt.wantCode {
				t.Fatalf("got %d, want %d: %s", rec.Code, tt.wantCode, rec.Body.String())
			}
			if tt.wantCode != http.StatusOK {
				return
			}
			var payload struct {
				Cost  float64 `json:"cost"`
				Known bool    `json:"known"`
			}
			decode(t, rec, &payload)
			if payload.Cost != tt.want {
				t.Errorf("cost = %v, want the pricing table's %v", payload.Cost, tt.want)
			}
			if strings.Contains(tt.name, "unknown") && payload.Known {
				t.Error("known = true for an unknown model")
			}
		})
	}
	if rec := serve(mux, http.MethodGet, "/api/cost/calc", ""); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET = %d, want 405", rec.Code)
	}
}

// Every read route goes through the auth guard; subscription usage through
// the localhost guard instead.
func TestRoutesUseServerGuards(t *testing.T) {
	var auth, local atomic.Int32
	deny := func(n *atomic.Int32) func(http.HandlerFunc) http.HandlerFunc {
		return func(http.HandlerFunc) http.HandlerFunc {
			return func(w http.ResponseWriter, _ *http.Request) {
				n.Add(1)
				http.Error(w, "denied", http.StatusUnauthorized)
			}
		}
	}
	mux := routes(NewModule(Deps{Guards: module.Guards{Auth: deny(&auth), Localhost: deny(&local)}}))
	for _, path := range []string{"/api/stats", "/api/analytics/overview", "/api/metrics", "/api/permission-stats", "/api/analytics/ui-usage"} {
		if rec := serve(mux, http.MethodGet, path, ""); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s = %d, want the auth guard's 401", path, rec.Code)
		}
	}
	for _, path := range []string{"/api/ui-usage", "/api/cost/calc"} {
		if rec := serve(mux, http.MethodPost, path, "{}"); rec.Code != http.StatusUnauthorized {
			t.Errorf("POST %s = %d, want the auth guard's 401", path, rec.Code)
		}
	}
	if rec := serve(mux, http.MethodGet, "/api/subscription-usage", ""); rec.Code != http.StatusUnauthorized || local.Load() != 1 {
		t.Fatalf("subscription usage = %d (localhost guard calls %d)", rec.Code, local.Load())
	}
	// A wrong method answers 405 before any guard runs.
	before := auth.Load()
	if rec := serve(mux, http.MethodPost, "/api/stats", ""); rec.Code != http.StatusMethodNotAllowed || auth.Load() != before {
		t.Fatalf("POST /api/stats = %d, auth guard calls %d → %d", rec.Code, before, auth.Load())
	}
}

func TestDirParam(t *testing.T) {
	for in, want := range map[string]string{"": "", "   ": "", "/repo/foo": "/repo/foo", "  /repo/foo  ": "/repo/foo", "/repo/foo/": "/repo/foo", "/": "/"} {
		r := httptest.NewRequest(http.MethodGet, "/?dir="+url.QueryEscape(in), nil)
		if got := dirParam(r); got != want {
			t.Errorf("dirParam(%q) = %q, want %q", in, got, want)
		}
	}
}

// Run samples database sizes at once, keeps working until cancelled, and
// returns only after its workers have, so nothing queries a closed database.
func TestRunSamplesAndStopsOnCancel(t *testing.T) {
	f := newFixture(t)
	f.getStats = func(context.Context) (*db.Stats, error) { return &db.Stats{}, nil }
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); f.Run(ctx) }()

	deadline := time.Now().Add(5 * time.Second)
	for {
		samples, err := f.deps.Store.DatabaseSizeSamples(t.Context(), 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(samples) == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("samples = %+v, want an opencode and an ocman sample", samples)
		}
		time.Sleep(5 * time.Millisecond)
	}
	select {
	case <-done:
		t.Fatal("Run returned before cancellation")
	case <-time.After(20 * time.Millisecond):
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancellation")
	}
}
