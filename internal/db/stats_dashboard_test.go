package db

import (
	"testing"
	"time"
)

// FR-10: dedicated regression tests for GetMetricsDashboard. The
// existing TestGetMetricsDashboard{,SessionAggregation} cover happy
// paths but don't pin the time-series shape, the empty-DB
// zero-fill, or the cumulative-cost monotonicity contract.

// TestGetMetricsDashboard_DailyGranularity_30Day pins the 30-day
// daily-bucket shape. Two assistant messages on adjacent days produce
// a 30-bucket series with two non-zero entries.
func TestGetMetricsDashboard_DailyGranularity_30Day(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	// Anchor "now" to a fixed moment (midnight UTC) so the bucket
	// math is reproducible.
	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC).UnixMilli()
	dayMs := int64(24 * 60 * 60 * 1000)

	insertSession(t, db, "s1", "session", "/p", now, now)
	insertMessage(t, db, "m1", "s1", now-dayMs, map[string]interface{}{
		"role":   "assistant",
		"finish": "end_turn",
		"cost":   0.10,
		"time":   map[string]interface{}{"created": now - dayMs - 1000, "completed": now - dayMs},
		"tokens": map[string]interface{}{"input": 100, "output": 50},
	})
	insertMessage(t, db, "m2", "s1", now-2*dayMs, map[string]interface{}{
		"role":   "assistant",
		"finish": "end_turn",
		"cost":   0.05,
		"time":   map[string]interface{}{"created": now - 2*dayMs - 1000, "completed": now - 2*dayMs},
		"tokens": map[string]interface{}{"input": 50, "output": 25},
	})

	dash, err := db.GetMetricsDashboard(t.Context(), MetricsDashboardOptions{
		Days: 30, RequestLimit: 50, SessionLimit: 50, ProjectLimit: 50,
	})

	if err != nil {
		t.Fatalf("GetMetricsDashboard: %v", err)
	}
	// Daily granularity for `days <= 7` is hourly, so 30 should be
	// daily — and we expect 30 buckets (zero-filled).
	if len(dash.Series) == 0 {
		t.Fatalf("Series is empty; want 30 daily buckets")
	}
	// Each bucket label for daily granularity is "YYYY-MM-DD".
	for _, p := range dash.Series {
		if len(p.Label) != len("2026-05-15") {
			t.Errorf("daily bucket label %q has unexpected shape (want YYYY-MM-DD)", p.Label)
			break
		}
	}
}

// TestGetMetricsDashboard_HourlyGranularity_24h asserts that a
// 24h-or-less window uses hourly buckets and that the bucket labels
// follow the "YYYY-MM-DD HH" shape.
func TestGetMetricsDashboard_HourlyGranularity_24h(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	now := time.Now().UnixMilli()
	insertSession(t, db, "s1", "session", "/p", now, now)
	insertMessage(t, db, "m1", "s1", now-30*60*1000, map[string]interface{}{
		"role":   "assistant",
		"finish": "end_turn",
		"cost":   0.01,
		"time":   map[string]interface{}{"created": now - 30*60*1000 - 500, "completed": now - 30*60*1000},
		"tokens": map[string]interface{}{"input": 10, "output": 5},
	})

	dash, err := db.GetMetricsDashboard(t.Context(), MetricsDashboardOptions{
		Days: 1, RequestLimit: 50, SessionLimit: 50, ProjectLimit: 50,
	})

	if err != nil {
		t.Fatalf("GetMetricsDashboard: %v", err)
	}
	if len(dash.Series) == 0 {
		t.Fatalf("Series is empty; want hourly buckets for 1d window")
	}
	// Labels are "YYYY-MM-DD HH" (13 chars) for hourly granularity.
	if got := len(dash.Series[0].Label); got != len("2026-05-15 14") {
		t.Errorf("hourly bucket label %q has length %d, want %d (YYYY-MM-DD HH)",
			dash.Series[0].Label, got, len("2026-05-15 14"))
	}
}

// TestGetMetricsDashboard_EmptyDB_ZeroFilledBuckets is the regression
// guard for the contract that empty-DB / no-matching-rows still
// returns a well-shaped Series rather than nil — the frontend
// charting library expects an array, and a nil series breaks the
// "no data" placeholder rendering.
func TestGetMetricsDashboard_EmptyDB_ZeroFilledBuckets(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	dash, err := db.GetMetricsDashboard(t.Context(), MetricsDashboardOptions{
		Days: 30, RequestLimit: 50, SessionLimit: 50, ProjectLimit: 50,
	})

	if err != nil {
		t.Fatalf("GetMetricsDashboard: %v", err)
	}
	if len(dash.Series) == 0 {
		t.Fatalf("Series len = 0; want 30 zero-filled daily buckets")
	}
	for i, p := range dash.Series {
		if p.Count != 0 {
			t.Errorf("bucket %d count = %d, want 0 (empty DB)", i, p.Count)
		}
		if p.CumulativeCost != 0 {
			t.Errorf("bucket %d cumulative cost = %v, want 0", i, p.CumulativeCost)
		}
	}
}

// TestGetMetricsDashboard_CumulativeCostMonotonic asserts the
// CumulativeCost series only ever increases.
func TestGetMetricsDashboard_CumulativeCostMonotonic(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	now := time.Now().UnixMilli()
	dayMs := int64(24 * 60 * 60 * 1000)
	insertSession(t, db, "s1", "session", "/p", now, now)
	// Three messages on three separate days, with non-zero costs.
	for i := 0; i < 3; i++ {
		when := now - int64(i)*dayMs
		insertMessage(t, db, "m"+string(rune('0'+i)), "s1", when, map[string]interface{}{
			"role":   "assistant",
			"finish": "end_turn",
			"cost":   0.10,
			"time":   map[string]interface{}{"created": when - 500, "completed": when},
			"tokens": map[string]interface{}{"input": 10, "output": 5},
		})
	}

	dash, err := db.GetMetricsDashboard(t.Context(), MetricsDashboardOptions{
		Days: 30, RequestLimit: 50, SessionLimit: 50, ProjectLimit: 50,
	})

	if err != nil {
		t.Fatalf("GetMetricsDashboard: %v", err)
	}
	prev := -1.0
	for i, p := range dash.Series {
		if p.CumulativeCost < prev {
			t.Errorf("CumulativeCost decreased at bucket %d: %v < %v", i, p.CumulativeCost, prev)
		}
		prev = p.CumulativeCost
	}
}

// TestGetMetricsDashboard_ProjectFilter_NarrowsResult covers the dir
// filter: only messages in sessions under that directory are counted.
func TestGetMetricsDashboard_ProjectFilter_NarrowsResult(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	now := time.Now().UnixMilli()
	insertSession(t, db, "s1", "in", "/proj/a", now, now)
	insertSession(t, db, "s2", "out", "/proj/b", now, now)
	insertMessage(t, db, "m1", "s1", now, map[string]interface{}{
		"role": "assistant", "finish": "end_turn", "cost": 0.1,
		"time": map[string]interface{}{"created": now - 500, "completed": now},
	})
	insertMessage(t, db, "m2", "s2", now, map[string]interface{}{
		"role": "assistant", "finish": "end_turn", "cost": 99.0,
		"time": map[string]interface{}{"created": now - 500, "completed": now},
	})

	dash, err := db.GetMetricsDashboard(t.Context(), MetricsDashboardOptions{
		Days: 30, RequestLimit: 50, SessionLimit: 50, ProjectLimit: 50,
		Dir: "/proj/a",
	})

	if err != nil {
		t.Fatalf("GetMetricsDashboard: %v", err)
	}
	if dash.Summary.Requests != 1 {
		t.Errorf("Requests = %d, want 1 (filter should exclude /proj/b)", dash.Summary.Requests)
	}
	if dash.Summary.TotalCost != 0.1 {
		t.Errorf("TotalCost = %v, want 0.1 (filter should exclude expensive /proj/b session)", dash.Summary.TotalCost)
	}
}

// TestGetMetricsDashboard_SessionLog_RespectsLimitAndCostSort asserts
// FR-10's contract: the Sessions slice is bounded by sessionLimit
// and the entries are sorted by total Cost descending so users can
// see the most expensive sessions first.
func TestGetMetricsDashboard_SessionLog_RespectsLimitAndCostSort(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	now := time.Now().UnixMilli()
	// Three sessions with descending costs: cheap < mid < expensive.
	costs := map[string]float64{"cheap": 0.01, "mid": 1.0, "expensive": 10.0}
	for sid, cost := range costs {
		insertSession(t, db, sid, sid, "/p", now, now)
		insertMessage(t, db, "m-"+sid, sid, now, map[string]interface{}{
			"role":   "assistant",
			"finish": "end_turn",
			"cost":   cost,
			"time":   map[string]interface{}{"created": now - 500, "completed": now},
			"tokens": map[string]interface{}{"input": 10, "output": 5},
		})
	}

	dash, err := db.GetMetricsDashboard(t.Context(), MetricsDashboardOptions{
		Days: 30, RequestLimit: 50, SessionLimit: 2, ProjectLimit: 50,
	})

	if err != nil {
		t.Fatalf("GetMetricsDashboard: %v", err)
	}
	if dash.TotalSessions != 3 {
		t.Errorf("TotalSessions = %d, want 3", dash.TotalSessions)
	}
	if len(dash.Sessions) != 2 {
		t.Fatalf("Sessions len = %d, want 2 (limit)", len(dash.Sessions))
	}
	// Cost descending → expensive then mid.
	if dash.Sessions[0].ID != "expensive" {
		t.Errorf("top session = %q, want %q (highest cost)", dash.Sessions[0].ID, "expensive")
	}
	if dash.Sessions[1].ID != "mid" {
		t.Errorf("second session = %q, want %q", dash.Sessions[1].ID, "mid")
	}
	if dash.Sessions[0].Cost < dash.Sessions[1].Cost {
		t.Errorf("Sessions not sorted by cost desc: %v < %v",
			dash.Sessions[0].Cost, dash.Sessions[1].Cost)
	}
}

// TestGetMetricsDashboard_SessionLog_TieBreaksByRecency confirms the
// secondary sort key. When two sessions share the same Cost (common
// for zero-cost subscription-plan sessions), the more recently
// active one wins.
func TestGetMetricsDashboard_SessionLog_TieBreaksByRecency(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	now := time.Now().UnixMilli()
	// Two sessions with identical Cost; differ only by activity time.
	insertSession(t, db, "older", "older", "/p", now-1000, now-1000)
	insertSession(t, db, "newer", "newer", "/p", now, now)
	for _, sid := range []string{"older", "newer"} {
		ts := now
		if sid == "older" {
			ts = now - 1000
		}
		insertMessage(t, db, "m-"+sid, sid, ts, map[string]interface{}{
			"role":   "assistant",
			"finish": "end_turn",
			"cost":   0.0, // identical
			"time":   map[string]interface{}{"created": ts - 500, "completed": ts},
			"tokens": map[string]interface{}{"input": 10, "output": 5},
		})
	}

	dash, err := db.GetMetricsDashboard(t.Context(), MetricsDashboardOptions{
		RequestLimit: 50, SessionLimit: 50, ProjectLimit: 50,
	})

	if err != nil {
		t.Fatalf("GetMetricsDashboard: %v", err)
	}
	if dash.Sessions[0].ID != "newer" {
		t.Errorf("Sessions[0].ID = %q, want %q (tie-break by recency)", dash.Sessions[0].ID, "newer")
	}
}

// TestGetMetricsDashboard_CostByModel_BasicSplit asserts that requests
// from two different models produce two stacked series whose final
// cumulative totals equal each model's total platform-reported cost.
func TestGetMetricsDashboard_CostByModel_BasicSplit(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	now := time.Now().UnixMilli()
	insertSession(t, db, "s1", "session", "/p", now, now)

	// Two requests with model A, total cost 0.30.
	insertMessage(t, db, "a1", "s1", now-2000, map[string]interface{}{
		"role": "assistant", "finish": "end_turn", "cost": 0.10,
		"providerID": "anthropic", "modelID": "claude-sonnet-4",
		"time": map[string]interface{}{"created": now - 2500, "completed": now - 2000},
	})
	insertMessage(t, db, "a2", "s1", now-1000, map[string]interface{}{
		"role": "assistant", "finish": "end_turn", "cost": 0.20,
		"providerID": "anthropic", "modelID": "claude-sonnet-4",
		"time": map[string]interface{}{"created": now - 1500, "completed": now - 1000},
	})
	// One request with model B, cost 0.05.
	insertMessage(t, db, "b1", "s1", now, map[string]interface{}{
		"role": "assistant", "finish": "end_turn", "cost": 0.05,
		"providerID": "openai", "modelID": "gpt-4",
		"time": map[string]interface{}{"created": now - 500, "completed": now},
	})

	dash, err := db.GetMetricsDashboard(t.Context(), MetricsDashboardOptions{
		Days: 30, RequestLimit: 50, SessionLimit: 50, ProjectLimit: 50,
	})

	if err != nil {
		t.Fatalf("GetMetricsDashboard: %v", err)
	}
	cbm := dash.CostByModel
	if len(cbm.Models) != 2 {
		t.Fatalf("CostByModel.Models len = %d, want 2; got %v", len(cbm.Models), cbm.Models)
	}
	// Top model (highest total) should rank first.
	if cbm.Models[0] != "anthropic/claude-sonnet-4" {
		t.Errorf("CostByModel.Models[0] = %q, want %q", cbm.Models[0], "anthropic/claude-sonnet-4")
	}
	if cbm.Models[1] != "openai/gpt-4" {
		t.Errorf("CostByModel.Models[1] = %q, want %q", cbm.Models[1], "openai/gpt-4")
	}
	if len(cbm.Series) != len(dash.Series) {
		t.Fatalf("CostByModel.Series len = %d, want %d (aligned with dashboard.Series)",
			len(cbm.Series), len(dash.Series))
	}
	// Final bucket should hold the full cumulative totals.
	last := cbm.Series[len(cbm.Series)-1]
	if len(last.Costs) != 2 {
		t.Fatalf("final ModelCostPoint.Costs len = %d, want 2", len(last.Costs))
	}
	if got, want := last.Costs[0], 0.30; floatNotClose(got, want) {
		t.Errorf("final cumulative cost for top model = %v, want %v", got, want)
	}
	if got, want := last.Costs[1], 0.05; floatNotClose(got, want) {
		t.Errorf("final cumulative cost for second model = %v, want %v", got, want)
	}
}

func TestGetMetricsDashboard_CostByModel_UsesEstimatedCostWhenReportedCostIsZero(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	now := time.Now().UnixMilli()
	insertSession(t, db, "s1", "session", "/p", now, now)
	for i, model := range []string{"gpt-5.6-sol", "terra"} {
		insertMessage(t, db, model, "s1", now+int64(i), map[string]interface{}{
			"role":       "assistant",
			"finish":     "end_turn",
			"providerID": "openai",
			"modelID":    model,
			"cost":       0,
			"tokens":     map[string]interface{}{"input": 100, "output": 50},
		})
	}

	dash, err := db.GetMetricsDashboard(t.Context(), MetricsDashboardOptions{
		Days: 30, Pricing: stubPricing{inputRate: 0.01, outputRate: 0.02},
	})

	if err != nil {
		t.Fatalf("GetMetricsDashboard: %v", err)
	}
	if got := dash.CostByModel.Models; len(got) != 2 || got[0] != "openai/gpt-5.6-sol" || got[1] != "openai/terra" {
		t.Fatalf("CostByModel.Models = %v, want GPT Sol and Terra", got)
	}
	last := dash.CostByModel.Series[len(dash.CostByModel.Series)-1]
	for i, got := range last.Costs {
		if want := 2.0; floatNotClose(got, want) {
			t.Errorf("final cumulative cost for %s = %v, want %v", dash.CostByModel.Models[i], got, want)
		}
	}
}

// TestGetMetricsDashboard_CostByModel_TopNAndOther verifies the "Top N
// + Other" rollup: when more than costByModelTopN distinct models
// appear, the lower-ranked ones are folded into a single trailing
// "Other" bucket and the total cost is preserved.
func TestGetMetricsDashboard_CostByModel_TopNAndOther(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	now := time.Now().UnixMilli()
	insertSession(t, db, "s1", "session", "/p", now, now)

	// costByModelTopN+2 models, descending cost so ranking is stable.
	// Top N: each gets a unique row; the last 2 should roll into Other.
	totalModels := costByModelTopN + 2
	expectTopCost := 0.0
	expectOtherCost := 0.0
	for i := 0; i < totalModels; i++ {
		cost := float64(totalModels-i) * 0.10 // descending
		if i < costByModelTopN {
			expectTopCost += cost
		} else {
			expectOtherCost += cost
		}
		insertMessage(t, db, "m"+string(rune('a'+i)), "s1", now-int64(i)*1000, map[string]interface{}{
			"role": "assistant", "finish": "end_turn", "cost": cost,
			"providerID": "p",
			"modelID":    "model-" + string(rune('a'+i)),
			"time":       map[string]interface{}{"created": now - int64(i)*1000 - 500, "completed": now - int64(i)*1000},
		})
	}

	dash, err := db.GetMetricsDashboard(t.Context(), MetricsDashboardOptions{
		Days: 30, RequestLimit: 50, SessionLimit: 50, ProjectLimit: 50,
	})

	if err != nil {
		t.Fatalf("GetMetricsDashboard: %v", err)
	}
	cbm := dash.CostByModel
	if got, want := len(cbm.Models), costByModelTopN+1; got != want {
		t.Fatalf("CostByModel.Models len = %d, want %d (top N + Other)", got, want)
	}
	if cbm.Models[len(cbm.Models)-1] != costByModelOtherKey {
		t.Errorf("last model = %q, want %q (Other rollup must trail)",
			cbm.Models[len(cbm.Models)-1], costByModelOtherKey)
	}

	last := cbm.Series[len(cbm.Series)-1]
	topSum := 0.0
	for i := 0; i < costByModelTopN; i++ {
		topSum += last.Costs[i]
	}
	if floatNotClose(topSum, expectTopCost) {
		t.Errorf("top-N cumulative sum = %v, want %v", topSum, expectTopCost)
	}
	if floatNotClose(last.Costs[len(last.Costs)-1], expectOtherCost) {
		t.Errorf("Other cumulative sum = %v, want %v",
			last.Costs[len(last.Costs)-1], expectOtherCost)
	}
}

// TestGetMetricsDashboard_CostByModel_EmptyDB asserts the contract
// that an empty DB still returns a well-shaped (empty-models)
// payload: the frontend treats this field as always-present.
func TestGetMetricsDashboard_CostByModel_EmptyDB(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	dash, err := db.GetMetricsDashboard(t.Context(), MetricsDashboardOptions{
		Days: 30, RequestLimit: 50, SessionLimit: 50, ProjectLimit: 50,
	})

	if err != nil {
		t.Fatalf("GetMetricsDashboard: %v", err)
	}
	if dash.CostByModel.Models == nil {
		t.Errorf("CostByModel.Models = nil, want empty slice")
	}
	if len(dash.CostByModel.Models) != 0 {
		t.Errorf("CostByModel.Models len = %d, want 0", len(dash.CostByModel.Models))
	}
	if len(dash.CostByModel.Series) != len(dash.Series) {
		t.Errorf("CostByModel.Series len = %d, want %d", len(dash.CostByModel.Series), len(dash.Series))
	}
}

func floatNotClose(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d > 1e-9
}
