package analytics

import (
	"net/http"
	"testing"
	"time"
)

func TestHistoryHealthIsDiagnosticAndNeverRequiresCompletedImport(t *testing.T) {
	f, h, _ := analyticsParityFixture(t)
	for _, tc := range []struct {
		name, status string
		ids          []string
		complete     bool
	}{
		{"initializing", "syncing", nil, false},
		{"partial", "syncing", []string{"one"}, false},
		{"complete", "ready", []string{"one"}, true},
		{"empty", "syncing", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h.Instances = func() ([]string, bool) { return tc.ids, tc.complete }
			h.CheckedAt = func() time.Time { return time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC) }
			var got struct {
				Status, LastCheckedAt string
				Sources               int
			}
			decode(t, f.serve(http.MethodGet, "/api/analytics/history-health", ""), &got)
			if got.Status != tc.status || got.Sources != len(tc.ids) || got.LastCheckedAt == "" {
				t.Fatalf("health: %+v", got)
			}
		})
	}
	h.CheckedAt = nil
	f.deps.HistoryAnalytics = nil
	var got struct{ Status string }
	decode(t, f.serve(http.MethodGet, "/api/analytics/history-health", ""), &got)
	if got.Status != "unavailable" {
		t.Fatalf("missing history reported healthy: %+v", got)
	}
}
