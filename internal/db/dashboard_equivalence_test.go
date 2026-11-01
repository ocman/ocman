package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"path/filepath"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/statev2"
)

// dashboardEquivalenceStore ingests a varied history through the public
// statev2 API: two sources, nested directories, untrimmed agents/models,
// reported and unreported costs, errors, tool timing, invalid rows, user
// turns and requests on hour, DST and future boundaries.
func dashboardEquivalenceStore(t *testing.T, now time.Time) (string, *statev2.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "dashboard.db")
	store, err := statev2.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	for _, id := range []string{"one", "two"} {
		if err := store.RegisterInstance(t.Context(), statev2.Instance{ID: id, OwnerID: "owner", Platform: "test", SourceKey: id}); err != nil {
			t.Fatal(err)
		}
	}
	rng := rand.New(rand.NewSource(7))
	agents := []string{"build", " build ", "plan", "", " plan", "explore"}
	providers := []string{"anthropic", "openai", ""}
	models := []string{"m1", "m2", " m3 ", ""}
	finishes := []string{"stop", "end_turn", "error", "tool-calls", "", " length "}
	dirs := []string{"/repo", "/repo/sub", "/repo-other", "/other"}
	fixed := []int64{
		time.Date(2026, 3, 8, 6, 59, 59, 0, time.UTC).UnixMilli(), // US DST start
		time.Date(2026, 3, 8, 7, 0, 0, 0, time.UTC).UnixMilli(),
		time.Date(2025, 11, 2, 5, 30, 0, 0, time.UTC).UnixMilli(), // US DST end
		time.Date(2025, 11, 2, 6, 30, 0, 0, time.UTC).UnixMilli(),
		time.Date(2026, 1, 1, 18, 14, 59, 999e6, time.UTC).UnixMilli(), // Kathmandu midnight
		time.Date(2026, 1, 1, 18, 15, 0, 0, time.UTC).UnixMilli(),
		now.Truncate(time.Hour).UnixMilli(),
		now.Truncate(time.Hour).UnixMilli() - 1,
		now.Add(-24 * time.Hour).Truncate(time.Hour).UnixMilli(),
		now.Add(-7 * 24 * time.Hour).UnixMilli(),
		now.Add(-30 * 24 * time.Hour).Add(-time.Minute).UnixMilli(),
		now.Add(2 * time.Hour).UnixMilli(), // clock skew: future request
	}
	for s := range 14 {
		instance := []string{"one", "two"}[s%2]
		key := statev2.Key{InstanceID: instance, SessionID: fmt.Sprintf("s%02d", s)}
		batch := statev2.Batch{Complete: true, Session: statev2.Session{Key: key, Title: fmt.Sprintf("Session %d", s),
			Directory: dirs[s%len(dirs)], CreatedAt: 1, UpdatedAt: now.UnixMilli()}}
		for m := range 40 {
			at := now.Add(-time.Duration(rng.Int63n(int64(45 * 24 * time.Hour)))).UnixMilli()
			if m < len(fixed) && s%3 == 0 {
				at = fixed[m]
			}
			id := fmt.Sprintf("m%02d", m)
			if m%9 == 0 {
				batch.Messages = append(batch.Messages, statev2.Message{ID: id, Position: int64(m), Role: "user", CreatedAt: at, Extensions: json.RawMessage(`{"role":"user"}`)})
				continue
			}
			if m%17 == 0 {
				batch.Messages = append(batch.Messages, statev2.Message{ID: id, Position: int64(m), Role: "assistant", CreatedAt: at, Extensions: json.RawMessage(`{"role":"assistant","tokens":"invalid"}`)})
				continue
			}
			data := map[string]any{
				"role": "assistant", "agent": agents[rng.Intn(len(agents))],
				"providerID": providers[rng.Intn(len(providers))], "modelID": models[rng.Intn(len(models))],
				"finish": finishes[rng.Intn(len(finishes))],
				"tokens": map[string]any{"input": rng.Intn(5000), "output": rng.Intn(2000),
					"cache": map[string]any{"read": rng.Intn(3) * rng.Intn(9000), "write": rng.Intn(2) * rng.Intn(700)}},
			}
			if rng.Intn(3) > 0 {
				data["cost"] = float64(rng.Intn(100000)) / 1e5
			}
			if rng.Intn(9) == 0 {
				data["error"] = map[string]any{"name": "APIError"}
			}
			var parts []statev2.Part
			if rng.Intn(4) > 0 {
				duration := int64(rng.Intn(60000) + 1)
				data["time"] = map[string]any{"created": at, "completed": at + duration}
				if rng.Intn(2) == 0 {
					start := at + duration/4
					tool, _ := json.Marshal(map[string]any{"tool": "bash", "state": map[string]any{"time": map[string]any{"start": start, "end": start + duration/3}}})
					parts = append(parts, statev2.Part{ID: id + "-tool", MessageID: id, Kind: "tool", Extensions: tool})
				}
			}
			raw, _ := json.Marshal(data)
			batch.Messages = append(batch.Messages, statev2.Message{ID: id, Position: int64(m), Role: "assistant", CreatedAt: at, Extensions: raw})
			batch.Parts = append(batch.Parts, parts...)
		}
		if _, err := store.Commit(t.Context(), batch); err != nil {
			t.Fatal(err)
		}
	}
	return path, store
}

func dashboardEquivalenceOptions(now time.Time) []MetricsDashboardOptions {
	identity := func(_ context.Context, instance, native string) (string, error) {
		if native == "s01" || native == "s02" {
			return "merged", nil // two sources' sessions share one canonical identity
		}
		return instance + ":" + native, nil
	}
	var out []MetricsDashboardOptions
	for _, days := range []int{0, 1, 7, 30} {
		var since int64
		if days > 0 {
			since = now.Add(-time.Duration(days) * 24 * time.Hour).UnixMilli()
		}
		for i, filter := range []struct{ agent, model, dir string }{
			{}, {agent: "build"}, {model: "anthropic/m1"}, {agent: "plan", model: "openai/m3", dir: "/repo"},
			{dir: "/repo"}, {agent: "missing"},
		} {
			opts := MetricsDashboardOptions{AgentFilter: filter.agent, ModelFilter: filter.model, Dir: filter.dir, Since: since, Days: days,
				RequestLimit: 7, RequestOffset: 3, SessionLimit: 5, SessionOffset: 1, ProjectLimit: 2}
			if i%2 == 1 {
				opts.Pricing = stubPricing{inputRate: 1e-6, outputRate: 3e-6, cacheReadRate: 1e-7, cacheWriteRate: 2e-6}
				opts.SessionIdentity = identity
				opts.RequestLimit, opts.RequestOffset, opts.SessionLimit, opts.SessionOffset, opts.ProjectOffset = 0, 0, 0, 0, 1
			}
			if i == 4 {
				opts.RequestOffset, opts.SessionOffset, opts.ProjectOffset = 10000, 10000, 10000
			}
			out = append(out, opts)
		}
	}
	return out
}

// TestDashboardSummariesMatchRequestScan compares every dashboard endpoint
// with the request-scanning implementation across local time zones.
func TestDashboardSummariesMatchRequestScan(t *testing.T) {
	now := time.Now()
	path, _ := dashboardEquivalenceStore(t, now)
	reader, err := OpenHistoryAnalytics(path, []string{"one", "two"})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	original := time.Local
	t.Cleanup(func() { time.Local = original })
	for _, zone := range []string{"UTC", "America/New_York", "Asia/Kathmandu", "Australia/Lord_Howe"} {
		loc, err := time.LoadLocation(zone)
		if err != nil {
			t.Fatalf("load %s: %v", zone, err)
		}
		time.Local = loc
		for _, opts := range dashboardEquivalenceOptions(now) {
			name := fmt.Sprintf("%s/days=%d/agent=%q/model=%q/dir=%q/priced=%t", zone, opts.Days, opts.AgentFilter, opts.ModelFilter, opts.Dir, opts.Pricing != nil)
			want, err := reader.legacyMetricsDashboard(t.Context(), opts)
			if err != nil {
				t.Fatal(err)
			}
			if want.TotalRequests == 0 && opts.AgentFilter != "missing" {
				t.Fatalf("%s: fixture has no requests", name)
			}
			got, err := reader.GetMetricsDashboard(t.Context(), opts)
			if err != nil {
				t.Fatal(err)
			}
			assertSameJSON(t, name+"/dashboard", want, got)
			performance, err := reader.GetMetricsPerformance(t.Context(), opts)
			if err != nil {
				t.Fatal(err)
			}
			assertSameJSON(t, name+"/performance", &MetricsPerformance{
				AvailableAgents: want.AvailableAgents, AvailableModels: want.AvailableModels, Summary: want.Summary, Series: want.Series,
				CostByModel: want.CostByModel, DailyEstimatedCostByModel: want.DailyEstimatedCostByModel,
				DailyEffectiveCostByModel: want.DailyEffectiveCostByModel, Agents: want.Agents, StopReasons: want.StopReasons,
			}, performance)
			for _, kind := range []MetricsLogKind{MetricsLogRequests, MetricsLogSessions, MetricsLogProjects} {
				log, err := reader.GetMetricsLog(t.Context(), opts, kind)
				if err != nil {
					t.Fatal(err)
				}
				expected := &MetricsLog{Kind: kind, AvailableAgents: want.AvailableAgents, AvailableModels: want.AvailableModels}
				switch kind {
				case MetricsLogRequests:
					expected.Total, expected.Requests = want.TotalRequests, want.Requests
				case MetricsLogSessions:
					expected.Total, expected.Sessions = want.TotalSessions, want.Sessions
				default:
					expected.Total, expected.Projects = want.TotalProjects, want.Projects
				}
				assertSameJSON(t, name+"/log/"+string(kind), expected, log)
			}
		}
	}
}

// assertSameJSON compares API payloads field by field. Floating-point sums
// may differ only by summation order, so numbers use a relative tolerance.
func assertSameJSON(t *testing.T, name string, want, got any) {
	t.Helper()
	decode := func(v any) any {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		var out any
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	if path, ok := sameJSON("", decode(want), decode(got)); !ok {
		w, _ := json.Marshal(want)
		g, _ := json.Marshal(got)
		t.Fatalf("%s differs at %s\nwant %s\n got %s", name, path, truncate(w), truncate(g))
	}
}

func truncate(raw []byte) string {
	if len(raw) > 4000 {
		return string(raw[:4000]) + "..."
	}
	return string(raw)
}

func sameJSON(path string, want, got any) (string, bool) {
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok || len(g) != len(w) {
			return path, false
		}
		for k, v := range w {
			if p, ok := sameJSON(path+"."+k, v, g[k]); !ok {
				return p, false
			}
		}
		return path, true
	case []any:
		g, ok := got.([]any)
		if !ok || len(g) != len(w) {
			return path, false
		}
		for i := range w {
			if p, ok := sameJSON(fmt.Sprintf("%s[%d]", path, i), w[i], g[i]); !ok {
				return p, false
			}
		}
		return path, true
	case float64:
		g, ok := got.(float64)
		return path, ok && math.Abs(w-g) <= 1e-9*math.Max(1, math.Max(math.Abs(w), math.Abs(g)))
	default:
		return path, want == got
	}
}

// TestDashboardSummaryReadsHourSummaries proves the summary comes from
// analytics_hour: removing facts behind full hours leaves totals unchanged.
func TestDashboardSummaryReadsHourSummaries(t *testing.T) {
	now := time.Now()
	path, store := dashboardEquivalenceStore(t, now)
	reader, err := OpenHistoryAnalytics(path, []string{"one", "two"})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	opts := MetricsDashboardOptions{Days: 30, Since: now.Add(-30 * 24 * time.Hour).UnixMilli()}
	before, err := reader.GetMetricsPerformance(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	_ = store.Close()
	writer, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	// Keep the partial first hour; the dashboard reads those exact facts.
	if _, err := writer.Exec(`DELETE FROM analytics_fact WHERE time_created>=?`, opts.Since-opts.Since%3600000+3600000); err != nil {
		t.Fatal(err)
	}
	after, err := reader.GetMetricsPerformance(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if before.Summary.Requests == 0 || after.Summary.Requests != before.Summary.Requests || after.Summary.TotalCost != before.Summary.TotalCost {
		t.Fatalf("summary scanned facts: before %d requests $%v, after %d requests $%v",
			before.Summary.Requests, before.Summary.TotalCost, after.Summary.Requests, after.Summary.TotalCost)
	}
}
