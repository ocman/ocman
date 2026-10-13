package claudeprovider

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
)

// openCodeShaped is an empty OpenCode database with a mirror, the shape
// analytics read Claude rows through.
func openCodeShaped(t *testing.T) *db.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "opencode.db")
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = raw.Exec(`CREATE TABLE session (id TEXT PRIMARY KEY, project_id TEXT NOT NULL DEFAULT '', parent_id TEXT,
		title TEXT NOT NULL DEFAULT '', directory TEXT NOT NULL DEFAULT '', time_created INTEGER NOT NULL DEFAULT 0,
		time_updated INTEGER NOT NULL DEFAULT 0);
	CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT NOT NULL, time_created INTEGER NOT NULL DEFAULT 0, data TEXT NOT NULL DEFAULT '{}');
	CREATE TABLE part (id TEXT PRIMARY KEY, message_id TEXT NOT NULL, session_id TEXT NOT NULL, time_created INTEGER NOT NULL DEFAULT 0, data TEXT NOT NULL DEFAULT '{}');`)
	raw.Close()
	if err != nil {
		t.Fatal(err)
	}
	d, err := db.OpenReadWrite(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if err := d.EnableAnalyticsMirror(filepath.Join(t.TempDir(), "analytics-cache.db"), path); err != nil {
		t.Fatal(err)
	}
	return d
}

// claudeOnly is the analytics store of an ocman without OpenCode.
func claudeOnly(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.OpenAnalyticsStore(filepath.Join(t.TempDir(), "analytics-cache.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestUsageViewsCountClaudeTranscriptTokens(t *testing.T) {
	for name, open := range map[string]func(*testing.T) *db.DB{"with OpenCode": openCodeShaped, "Claude only": claudeOnly} {
		t.Run(name, func(t *testing.T) { checkUsageViews(t, open(t)) })
	}
}

func checkUsageViews(t *testing.T, d *db.DB) {
	config := fixture(t)
	indexPath := filepath.Join(t.TempDir(), "claude-index.db")
	idx, err := OpenIndex(indexPath, config)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()
	if err := idx.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	entries, err := idx.query(t.Context(), "directory = '/work/repo'")
	if err != nil || len(entries) != 2 {
		t.Fatalf("entries = %d, %v", len(entries), err)
	}
	var wantIn, wantOut int64
	for _, e := range entries {
		wantIn, wantOut = wantIn+e.Input, wantOut+e.Output
	}

	d.AddAnalyticsSource(AnalyticsSource(indexPath))
	if err := d.SyncAnalyticsMirror(t.Context()); err != nil {
		t.Fatal(err)
	}
	dash, err := d.GetMetricsDashboard(t.Context(), db.MetricsDashboardOptions{Dir: "/work/repo"})
	if err != nil {
		t.Fatal(err)
	}
	s := dash.Summary
	// msg_A 10/5 (+100 cache read, 20 write), msg_B 7/3, msg_C 4/2, then the
	// subagent's 3/1 and 2/1: the transcripts' per-message usage.
	if s.InputTokens != 26 || s.OutputTokens != 12 || s.CacheReadTokens != 100 || s.CacheWriteTokens != 20 {
		t.Fatalf("summary = %+v", s)
	}
	if s.InputTokens != wantIn || s.OutputTokens != wantOut {
		t.Fatalf("usage %d/%d, index totals %d/%d", s.InputTokens, s.OutputTokens, wantIn, wantOut)
	}
	if dash.TotalRequests != 5 {
		t.Fatalf("requests = %d", dash.TotalRequests)
	}
	if s.TotalCost != 0 {
		t.Fatalf("reported cost = %v, want 0 so analytics price it API-equivalent", s.TotalCost)
	}
	// The subagent's Bash call carries its tool timing, so its tool-calls
	// turn gets a throughput sample instead of being skipped.
	timed := false
	for _, r := range dash.Requests {
		timed = timed || (r.StopReason == "tool-calls" && r.TokensPerSecond > 0)
	}
	if !timed {
		t.Fatalf("no tool-calls request has a throughput: %+v", dash.Requests)
	}
	// Priced at API rates: Cost stays the reported 0, EffectiveCost is the
	// API-equivalent figure the UI labels.
	priced, err := d.GetMetricsDashboard(t.Context(), db.MetricsDashboardOptions{Dir: "/work/repo", Pricing: perToken{}})
	if err != nil {
		t.Fatal(err)
	}
	if p := priced.Summary; p.TotalCost != 0 || p.TotalEffectiveCost != float64(26+12+100+20) {
		t.Fatalf("priced summary = %+v", p)
	}
	stats, err := d.GetStats(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if stats.TotalSessions != 3 || stats.TotalTokensIn != wantIn+5 {
		t.Fatalf("stats = %+v, want 3 sessions and %d input tokens", stats, wantIn+5)
	}
}

// TestAnalyticsSourceHandsOverLateIndexedRows: rows outside the time
// window come back when their transcript was indexed after changedSince.
func TestAnalyticsSourceHandsOverLateIndexedRows(t *testing.T) {
	indexPath := filepath.Join(t.TempDir(), "claude-index.db")
	idx, err := OpenIndex(indexPath, fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()
	before := time.Now().UnixMilli()
	if err := idx.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Hour).UnixMilli()
	src := AnalyticsSource(indexPath)
	_, late, err := src(t.Context(), future, before)
	if err != nil || len(late) == 0 {
		t.Fatalf("late rows = %d, %v", len(late), err)
	}
	_, none, err := src(t.Context(), future, future)
	if err != nil || len(none) != 0 {
		t.Fatalf("unchanged rows = %d, %v", len(none), err)
	}
}

func TestAnalyticsSourceWithoutIndexIsEmpty(t *testing.T) {
	sessions, messages, err := AnalyticsSource(filepath.Join(t.TempDir(), "missing.db"))(t.Context(), 0, 0)
	if err != nil || sessions != nil || messages != nil {
		t.Fatalf("got %v %v %v", sessions, messages, err)
	}
}

// perToken prices every token at $1.
type perToken struct{}

func (perToken) CalcCost(_ string, in, out, read, write int64) float64 {
	return float64(in + out + read + write)
}
