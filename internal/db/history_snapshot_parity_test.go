package db

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/pricing"
)

// The caller supplies a frozen validation snapshot, not the running store.
// Both readers then calculate against exactly the same committed history.
func TestHistorySummarySnapshotParity(t *testing.T) {
	path := os.Getenv("OCMAN_HISTORY_PARITY_DB")
	if path == "" {
		t.Skip("set OCMAN_HISTORY_PARITY_DB to a frozen compact validation snapshot")
	}
	ids := strings.Split(os.Getenv("OCMAN_HISTORY_LATENCY_INSTANCES"), ",")
	if ids[0] == "" {
		t.Fatal("source instance IDs are required")
	}
	history, err := OpenHistoryAnalytics(path, ids)
	if err != nil {
		t.Fatal(err)
	}
	defer history.Close()
	legacy := &DB{db: history.db}
	// A 90-day window uses the legacy scan path, not OpenCode's index-specific
	// recent-window CROSS JOIN on the statev2 diagnostic compatibility views.
	since, until := time.Now().AddDate(0, 0, -90).UnixMilli(), time.Now().UnixMilli()
	opts := MetricsDashboardOptions{Since: since, Days: 90, RequestLimit: 20, SessionLimit: 20, ProjectLimit: 20}
	opts.Pricing = pricing.Load()
	for _, query := range []struct {
		name string
		run  func(*DB) (any, error)
	}{
		{"stats", func(d *DB) (any, error) { return d.GetStats(t.Context()) }},
		{"models", func(d *DB) (any, error) { return d.GetModelUsage(t.Context(), since, "") }},
		{"activity", func(d *DB) (any, error) { return d.GetDailyActivity(t.Context(), since, "", "") }},
		// Let the fixed since dominate the method's wall-clock-based default.
		{"hourly-tokens", func(d *DB) (any, error) { return d.GetHourlyTokensByModel(t.Context(), 3650, since, "", "") }},
		{"performance", func(d *DB) (any, error) { return d.GetMetricsPerformance(t.Context(), opts) }},
		{"metrics", func(d *DB) (any, error) { return d.GetMetricsDashboard(t.Context(), opts) }},
		{"concurrency", func(d *DB) (any, error) { return d.GetSessionConcurrency(t.Context(), since, until, "") }},
		{"runtime", func(d *DB) (any, error) { return d.GetAgentRunHours(t.Context(), since, until, "", nil) }},
	} {
		t.Run(query.name, func(t *testing.T) {
			old, err := query.run(legacy)
			if err != nil {
				t.Fatal(err)
			}
			got, err := query.run(history)
			if err != nil {
				t.Fatal(err)
			}
			decode := func(v any) any {
				raw, err := json.Marshal(v)
				if err != nil {
					t.Fatal(err)
				}
				var decoded any
				if err := json.Unmarshal(raw, &decoded); err != nil {
					t.Fatal(err)
				}
				return decoded
			}
			if err := compareHistorySummaryJSON("$", decode(old), decode(got)); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func compareHistorySummaryJSON(path string, want, got any) error {
	switch a := want.(type) {
	case float64:
		b, ok := got.(float64)
		if !ok {
			return fmt.Errorf("%s: numeric type differs", path)
		}
		if a == b {
			return nil
		}
		if math.Trunc(a) == a && math.Trunc(b) == b {
			return fmt.Errorf("%s: integer totals differ: %v vs %v", path, a, b)
		}
		if math.Abs(a-b) > 1e-7+1e-12*math.Abs(a) {
			return fmt.Errorf("%s: values differ: %v vs %v", path, a, b)
		}
	case []any:
		b, ok := got.([]any)
		if !ok || len(a) != len(b) {
			return fmt.Errorf("%s: array length/type differs", path)
		}
		for i := range a {
			if err := compareHistorySummaryJSON(fmt.Sprintf("%s[%d]", path, i), a[i], b[i]); err != nil {
				return err
			}
		}
	case map[string]any:
		b, ok := got.(map[string]any)
		if !ok || len(a) != len(b) {
			return fmt.Errorf("%s: object fields differ", path)
		}
		for k, v := range a {
			other, ok := b[k]
			if !ok {
				return fmt.Errorf("%s.%s: missing field", path, k)
			}
			if err := compareHistorySummaryJSON(path+"."+k, v, other); err != nil {
				return err
			}
		}
	default:
		if want != got {
			return fmt.Errorf("%s: values differ: %v vs %v", path, want, got)
		}
	}
	return nil
}
