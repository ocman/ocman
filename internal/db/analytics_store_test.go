package db

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"
)

// claudeSource is a fake platform: one session with a prompt and one
// tool-calling assistant turn whose tool ran for 3 of its 4 seconds.
func claudeSource(now int64) AnalyticsSource {
	return func(_ context.Context, since, _ int64) ([]AnalyticsSession, []AnalyticsMessage, error) {
		msgs := []AnalyticsMessage{
			{ID: "cu", SessionID: "c1", TimeCreated: now - 9000, Settled: true, Data: `{"role":"user"}`},
			{ID: "ca", SessionID: "c1", TimeCreated: now - 8000, Settled: true,
				Data:      fmt.Sprintf(`{"role":"assistant","providerID":"anthropic","modelID":"claude-x","cost":0,"finish":"tool-calls","tokens":{"input":1000,"output":200,"cache":{"read":30,"write":4}},"time":{"created":%d,"completed":%d}}`, now-8000, now-4000),
				ToolTimes: []string{fmt.Sprintf(`{"start":%d,"end":%d}`, now-7000, now-4000)}},
		}
		out := msgs[:0:0]
		for _, m := range msgs {
			if m.TimeCreated >= since {
				out = append(out, m)
			}
		}
		return []AnalyticsSession{{ID: "c1", Directory: "/p/claude", Title: "Claude", TimeCreated: now - 10000}}, out, nil
	}
}

func checkClaudeOnly(t *testing.T, d *DB) {
	t.Helper()
	ctx := t.Context()
	stats, err := d.GetStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.TotalSessions != 1 || stats.TotalProjects != 1 || stats.TotalMessages != 1 || stats.TotalTokensIn != 1000 || stats.TotalTokensOut != 200 {
		t.Fatalf("stats = %+v", stats)
	}
	dash, err := d.GetMetricsDashboard(ctx, MetricsDashboardOptions{Dir: "/p/claude"})
	if err != nil {
		t.Fatal(err)
	}
	if dash.TotalRequests != 1 || dash.Summary.InputTokens != 1000 || dash.Summary.CacheReadTokens != 30 {
		t.Fatalf("dashboard = %+v", dash.Summary)
	}
	if dash.Summary.TotalCost != 0 {
		t.Fatalf("reported cost = %v, want 0 (priced as API-equivalent)", dash.Summary.TotalCost)
	}
	// 200 tokens over the 1 s the model ran, the tool's 3 s excluded.
	if len(dash.Requests) != 1 || dash.Requests[0].TokensPerSecond != 200 {
		t.Fatalf("requests = %+v", dash.Requests)
	}
	if len(dash.Sessions) != 1 || dash.Sessions[0].Title != "Claude" || dash.Sessions[0].Directory != "/p/claude" {
		t.Fatalf("sessions = %+v", dash.Sessions)
	}
	hourly, err := d.GetHourlyActivity(ctx, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	daily, err := d.GetDailyActivity(ctx, 0, "", "")
	if err != nil {
		t.Fatal(err)
	}
	sessions, dailySessions, dailyMessages := 0, 0, 0
	for _, h := range hourly {
		sessions += h.Sessions
	}
	for _, day := range daily {
		dailySessions, dailyMessages = dailySessions+day.Sessions, dailyMessages+day.Messages
	}
	if sessions != 1 || dailySessions != 1 || dailyMessages != 1 {
		t.Fatalf("hourly sessions = %d, daily sessions = %d, messages = %d", sessions, dailySessions, dailyMessages)
	}
}

// TestAnalyticsStoreWithoutOpenCode: a Claude-only ocman has analytics
// without any OpenCode database, also before its first sync and after a
// restart.
func TestAnalyticsStoreWithoutOpenCode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "analytics-cache.db")
	now := time.Now().UnixMilli()
	d, err := OpenAnalyticsStore(path)
	if err != nil {
		t.Fatal(err)
	}
	d.AddAnalyticsSource(claudeSource(now))
	checkClaudeOnly(t, d) // the first read waits for the first build
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}

	// Restart: the persisted copy serves at once while the source's
	// registration schedules a background rebuild.
	d, err = OpenAnalyticsStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if !d.mirror.ready.Load() {
		t.Fatal("restarted store not ready")
	}
	d.AddAnalyticsSource(claudeSource(now))
	checkClaudeOnly(t, d)
	if _, err := OpenAnalyticsStore(filepath.Join(t.TempDir(), "missing", "x.db")); err == nil {
		t.Fatal("bad path opened")
	}
}

// TestMixedAnalyticsNeverReadOpenCodeAlone: with another platform's rows
// in the mirror, no read (first build, failed rebuild, session counts) may
// answer from OpenCode alone.
func TestMixedAnalyticsNeverReadOpenCodeAlone(t *testing.T) {
	d, path := openMirrored(t)
	now := time.Now().UnixMilli()
	seedMirrorSource(t, d, now)
	d.AddAnalyticsSource(claudeSource(now))
	check := func(stage string) {
		t.Helper()
		stats, err := d.GetStats(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if stats.TotalSessions != 3 || stats.TotalProjects != 3 || stats.TotalTokensIn != 1107 {
			t.Fatalf("%s: stats = %+v", stage, stats)
		}
		dash, err := d.GetMetricsDashboard(t.Context(), MetricsDashboardOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if dash.TotalRequests != 3 || dash.TotalSessions != 3 {
			t.Fatalf("%s: requests = %d sessions = %d", stage, dash.TotalRequests, dash.TotalSessions)
		}
		hourly, err := d.GetHourlyActivity(t.Context(), 0, "")
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, h := range hourly {
			n += h.Sessions
		}
		if n != 3 {
			t.Fatalf("%s: hourly sessions = %d", stage, n)
		}
	}
	check("before the first build")

	// Full rebuilds are failing: read the last copy, not OpenCode.
	d.mirror.lastFull.Store(1)
	d.mirror.failedAt.Store(time.Now().UnixMilli())
	check("rebuild failing")

	// A failing incremental sync also keeps the last copy.
	d.mirror.lastFull.Store(time.Now().UnixMilli())
	d.mirror.failedAt.Store(0)
	forceStale(d)
	d.mirror.sourcesMu.Lock()
	good := d.mirror.sources[0]
	d.mirror.sources[0] = func(context.Context, int64, int64) ([]AnalyticsSession, []AnalyticsMessage, error) {
		return nil, nil, fmt.Errorf("index unreadable")
	}
	d.mirror.sourcesMu.Unlock()
	check("sync failing")
	d.mirror.sourcesMu.Lock()
	d.mirror.sources[0] = good
	d.mirror.sourcesMu.Unlock()

	// Restart: a new process reopens the persisted mirror; its first reads
	// (rebuild due in the background) still include Claude.
	if err := d.SyncAnalyticsMirror(t.Context()); err != nil {
		t.Fatal(err)
	}
	d = openTestDB(t)
	t.Cleanup(func() { d.Close() })
	d.db.SetMaxOpenConns(1)
	seedMirrorSource(t, d, now)
	if err := d.EnableAnalyticsMirror(path, "src-a"); err != nil {
		t.Fatal(err)
	}
	d.AddAnalyticsSource(claudeSource(now))
	check("after restart")
	if err := d.SyncAnalyticsMirror(t.Context()); err != nil {
		t.Fatal(err)
	}
	check("after the restart rebuild")

	// A never-built mirror whose build just failed waits out the retry
	// interval instead of rebuilding on every read.
	empty, _ := openMirrored(t)
	calls := 0
	empty.AddAnalyticsSource(func(context.Context, int64, int64) ([]AnalyticsSession, []AnalyticsMessage, error) {
		calls++
		return nil, nil, fmt.Errorf("boom")
	})
	empty.mirror.failedAt.Store(time.Now().UnixMilli())
	if h := empty.analytics(t.Context()); h != empty.mirror.db || calls != 0 {
		t.Fatalf("handle mirror=%v, source calls = %d", h == empty.mirror.db, calls)
	}
	empty.mirror.failedAt.Store(0)
	if h := empty.analytics(t.Context()); h != empty.mirror.db || calls != 1 {
		t.Fatalf("handle mirror=%v, source calls = %d", h == empty.mirror.db, calls)
	}
}

// TestAnalyticsSourceRowsChangedLateJoinIncrementalSync: a transcript
// indexed after the mirror's last sync carries old timestamps, outside the
// incremental window; changedSince still hands it over.
func TestAnalyticsSourceRowsChangedLateJoinIncrementalSync(t *testing.T) {
	d, _ := openMirrored(t)
	now := time.Now().UnixMilli()
	seedMirrorSource(t, d, now)
	var mu sync.Mutex
	var indexedAt int64 // 0: not indexed yet
	var changes []int64
	old := now - 30*24*3600*1000
	d.AddAnalyticsSource(func(_ context.Context, since, changedSince int64) ([]AnalyticsSession, []AnalyticsMessage, error) {
		mu.Lock()
		defer mu.Unlock()
		changes = append(changes, changedSince)
		if indexedAt == 0 {
			return nil, nil, nil
		}
		var msgs []AnalyticsMessage
		if old >= since || indexedAt >= changedSince {
			msgs = append(msgs, AnalyticsMessage{ID: "late", SessionID: "c1", TimeCreated: old, Settled: true, Data: `{"role":"assistant","tokens":{"input":5}}`})
		}
		return []AnalyticsSession{{ID: "c1", Directory: "/p/claude", TimeCreated: old}}, msgs, nil
	})
	if err := d.SyncAnalyticsMirror(t.Context()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	indexedAt = time.Now().UnixMilli()
	mu.Unlock()
	d.mirror.lastSync.Store(time.Now().Add(-mirrorMaxStaleness - time.Second).UnixNano())
	if err := d.SyncAnalyticsMirror(t.Context()); err != nil {
		t.Fatal(err)
	}
	if ids := mirrorIDs(t, d); !slices.Contains(ids, "late") {
		t.Fatalf("late-indexed row missing: %v (changedSince %v)", ids, changes)
	}
	if len(changes) != 2 || changes[0] != 0 || changes[1] <= 0 {
		t.Fatalf("changedSince = %v", changes)
	}
}
