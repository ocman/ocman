package db

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestModelDuration(t *testing.T) {
	for _, tc := range []struct {
		name       string
		start, end int64
		tools      []toolTiming
		want       int64
	}{
		{"plain", 1000, 11000, nil, 10000},
		{"invalid start", 0, 11000, nil, 0},
		{"invalid end", 1000, 1000, nil, 0},
		{"overlap", 1000, 11000, []toolTiming{{5000, 11000}, {3000, 9000}}, 2000},
		{"disjoint", 1000, 11000, []toolTiming{{2000, 3000}, {5000, 6000}}, 8000},
		{"clipped", 1000, 11000, []toolTiming{{100, 2000}, {10000, 15000}}, 8000},
		{"outside", 1000, 11000, []toolTiming{{100, 500}, {12000, 15000}}, 10000},
		{"missing", 1000, 11000, []toolTiming{{0, 5000}}, 0},
		{"unfinished", 1000, 11000, []toolTiming{{2000, 0}}, 0},
		{"reversed", 1000, 11000, []toolTiming{{5000, 2000}}, 0},
		{"all wait", 1000, 11000, []toolTiming{{1000, 11000}}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := modelDuration(tc.start, tc.end, tc.tools); got != tc.want {
				t.Fatalf("got %d, want %d", got, tc.want)
			}
		})
	}
}

func TestThroughputMissingTimingDoesNotDeflateAverages(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	insertSession(t, db, "s", "test", "/a", 1000, 11000)
	// Cross the batch boundary and include absent, null and malformed timing.
	for i := range 403 {
		id := fmt.Sprintf("m%d", i)
		finish := "stop"
		if i < 402 {
			finish = "tool-calls"
		}
		insertMessage(t, db, id, "s", 1000, map[string]any{
			"role": "assistant", "finish": finish,
			"time":   map[string]any{"created": 1000, "completed": 11000},
			"tokens": map[string]any{"output": 200},
		})
		if i < 401 {
			var timing any
			if i == 400 {
				timing = "bad"
			}
			insertPart(t, db, id, id, "s", 2000, map[string]any{"type": "tool", "state": map[string]any{"time": timing}})
		}
	}
	metrics, err := db.GetMetricsDashboard(t.Context(), MetricsDashboardOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if metrics.Summary.AvgTokensPerSec != 20 || metrics.Series[0].AvgOutputTokensSec != 20 || metrics.Sessions[0].AvgTokensPerSec != 20 || metrics.Projects[0].AvgTokensPerSec != 20 {
		t.Fatalf("unknown timing must be omitted from averages: %+v", metrics)
	}
	if _, err := db.db.Exec(`DROP TABLE part`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetMetricsDashboard(t.Context(), MetricsDashboardOptions{}); err == nil {
		t.Fatal("expected timing query failure")
	}
}

func TestThroughputExcludesToolWaits(t *testing.T) {
	for _, mirror := range []bool{false, true} {
		t.Run(map[bool]string{false: "source", true: "mirror"}[mirror], func(t *testing.T) {
			db := openTestDB(t)
			defer db.Close()
			insertSession(t, db, "s", "test", "/a", 1000, 11000)
			insertMessage(t, db, "m", "s", 1000, map[string]any{
				"role": "assistant", "finish": "tool-calls",
				"time":   map[string]any{"created": 1000, "completed": 11000},
				"tokens": map[string]any{"output": 200},
			})
			// Parallel tools overlap; the union is eight seconds, not fourteen.
			for id, span := range map[string][2]int64{"a": {3000, 11000}, "b": {5000, 11000}} {
				insertPart(t, db, id, "m", "s", span[0], map[string]any{
					"type": "tool", "state": map[string]any{"status": "completed", "time": map[string]any{"start": span[0], "end": span[1]}},
				})
			}
			if mirror {
				if err := db.EnableAnalyticsMirror(filepath.Join(t.TempDir(), "analytics.db"), "test"); err != nil {
					t.Fatal(err)
				}
				if err := db.SyncAnalyticsMirror(t.Context()); err != nil {
					t.Fatal(err)
				}
				// Warm reads must use the compact timing projection, even if
				// historical source payloads are no longer accessible.
				if _, err := db.db.Exec(`DROP TABLE part`); err != nil {
					t.Fatal(err)
				}
			}
			metrics, err := db.GetMetricsDashboard(t.Context(), MetricsDashboardOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if len(metrics.Requests) != 1 || metrics.Requests[0].TokensPerSecond != 100 {
				t.Fatalf("requests = %+v; want 100 output tok/s", metrics.Requests)
			}
			if metrics.Requests[0].DurationMs != 10000 {
				t.Fatal("request latency must retain tool time")
			}
			if mirror {
				if _, err := db.GetMetricsPerformance(t.Context(), MetricsDashboardOptions{}); err != nil {
					t.Fatalf("repeated warm read: %v", err)
				}
			}
		})
	}
}

func TestMirrorRefreshesToolTimings(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	now := time.Now().UnixMilli()
	insertSession(t, db, "s", "test", "/a", now, now)
	insertMessage(t, db, "m", "s", now, map[string]any{
		"role": "assistant", "time": map[string]any{"created": now, "completed": now + 10000},
		"tokens": map[string]any{"output": 200},
	})
	insertPart(t, db, "p", "m", "s", now, map[string]any{
		"type": "tool", "state": map[string]any{"time": map[string]any{"start": now + 2000}},
	})
	if err := db.EnableAnalyticsMirror(filepath.Join(t.TempDir(), "analytics.db"), "test"); err != nil {
		t.Fatal(err)
	}
	if err := db.SyncAnalyticsMirror(t.Context()); err != nil {
		t.Fatal(err)
	}
	check := func(want float64) {
		t.Helper()
		metrics, err := db.GetMetricsPerformance(t.Context(), MetricsDashboardOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if metrics.Summary.AvgTokensPerSec != want {
			t.Fatalf("got %v, want %v", metrics.Summary.AvgTokensPerSec, want)
		}
	}
	check(0) // Missing end remains unknown after projection.
	if _, err := db.db.Exec(`UPDATE part SET data = json_set(data, '$.state.time.end', ?)`, now+10000); err != nil {
		t.Fatal(err)
	}
	db.mirror.lastSync.Store(0)
	if err := db.SyncAnalyticsMirror(t.Context()); err != nil {
		t.Fatal(err)
	}
	check(100)
	if _, err := db.db.Exec(`DELETE FROM part`); err != nil {
		t.Fatal(err)
	}
	db.mirror.lastSync.Store(0)
	if err := db.SyncAnalyticsMirror(t.Context()); err != nil {
		t.Fatal(err)
	}
	check(20) // A deleted part must not leave a cached wait behind.
}
