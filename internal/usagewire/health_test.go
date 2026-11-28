package usagewire

import (
	"testing"
	"time"
)

func TestHealthReportValidation(t *testing.T) {
	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		edit func(*Report)
		want bool
	}{
		{"valid without latency", func(*Report) {}, true},
		{"all three platforms", func(r *Report) {
			r.Health = append(r.Health, Health{Platform: "claude", Starts: StartCounts{Ready: 1}}, Health{Platform: "opencode", Starts: StartCounts{Ready: 1}})
		}, true},
		{"maximum count and exact bucket length", func(r *Report) {
			r.Health[0].Starts.Ready = 1_000_000
			r.Health[0].Turns.FirstResponse = []int64{0, 0, 0, 0, 0, 0, 1}
		}, true},
		{"empty rows", func(r *Report) { r.Health = []Health{} }, false},
		{"too many rows", func(r *Report) { r.Health = make([]Health, 4) }, false},
		{"unknown platform", func(r *Report) { r.Health[0].Platform = "custom" }, false},
		{"duplicate platform", func(r *Report) { r.Health = append(r.Health, r.Health[0]) }, false},
		{"short histogram", func(r *Report) { r.Health[0].Turns.FirstResponse = make([]int64, 6) }, false},
		{"long histogram", func(r *Report) { r.Health[0].Turns.FirstResponse = make([]int64, 8) }, false},
		{"negative counter", func(r *Report) { r.Health[0].Starts.Ready = -1 }, false},
		{"counter above maximum", func(r *Report) { r.Health[0].Turns.Failed = 1_000_001 }, false},
		{"negative bucket", func(r *Report) { r.Health[0].Turns.FirstResponse = []int64{0, 0, 0, 0, 0, 0, -1} }, false},
		{"zero-total row", func(r *Report) { r.Health[0].Starts.Ready = 0 }, false},
		{"open day", func(r *Report) { r.Date = "2026-11-28" }, false},
		{"mixed report types", func(r *Report) { r.Daily = &Daily{} }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := Report{
				InstallationID: "3f70f326-5593-4b78-a31f-c67a23920334",
				Date:           "2026-11-27",
				Version:        "0.275.0",
				Health:         []Health{{Platform: "codex", Starts: StartCounts{Ready: 1}}},
			}
			tc.edit(&r)
			if got := r.Valid(now); got != tc.want {
				t.Fatalf("Report.Valid() = %t, want %t", got, tc.want)
			}
		})
	}
}
