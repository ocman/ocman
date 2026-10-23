package db

import (
	"fmt"
	"testing"
	"time"
)

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
	reader := storedFixture(t, db)
	metrics, err := reader.GetMetricsDashboard(t.Context(), MetricsDashboardOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if metrics.Summary.AvgTokensPerSec != 20 || metrics.Series[0].AvgOutputTokensSec != 20 || metrics.Sessions[0].AvgTokensPerSec != 20 || metrics.Projects[0].AvgTokensPerSec != 20 {
		t.Fatalf("unknown timing must be omitted from averages: %+v", metrics)
	}
	if _, err := reader.db.Exec(`DROP VIEW history_fact`); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.GetMetricsDashboard(t.Context(), MetricsDashboardOptions{}); err == nil {
		t.Fatal("expected retained timing query failure")
	}
}

func TestThroughputExcludesToolWaits(t *testing.T) {
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
	metrics, err := storedFixture(t, db).GetMetricsDashboard(t.Context(), MetricsDashboardOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(metrics.Requests) != 1 || metrics.Requests[0].TokensPerSecond != 100 {
		t.Fatalf("requests = %+v; want 100 output tok/s", metrics.Requests)
	}
	if metrics.Requests[0].DurationMs != 10000 {
		t.Fatal("request latency must retain tool time")
	}
}

func TestThroughputRefreshesToolTimings(t *testing.T) {
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
	check := func(want float64) {
		t.Helper()
		metrics, err := storedFixture(t, db).GetMetricsPerformance(t.Context(), MetricsDashboardOptions{})
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
	check(100)
	if _, err := db.db.Exec(`DELETE FROM part`); err != nil {
		t.Fatal(err)
	}
	check(20) // A deleted part must not leave a cached wait behind.
}
