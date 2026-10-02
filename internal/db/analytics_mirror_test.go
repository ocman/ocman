package db

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func seedMirrorSource(t *testing.T, d *DB, now int64) {
	t.Helper()
	day := int64(24 * time.Hour / time.Millisecond)
	insertSession(t, d, "s1", "One", "/p/a", now-60*day, now)
	insertSession(t, d, "s2", "Helper (@explore subagent)", "/p/b", now-2*day, now)
	insertMessage(t, d, "u-old", "s1", now-50*day, map[string]any{"role": "user", "attachment": strings.Repeat("x", 4096)})
	insertMessage(t, d, "a-old", "s1", now-50*day+1, map[string]any{
		"role": "assistant", "agent": "build", "providerID": "anthropic", "modelID": "opus", "cost": 0.5, "finish": "stop",
		"tokens": map[string]any{"input": 100, "output": 20, "cache": map[string]any{"read": 5, "write": 1}},
		"time":   map[string]any{"created": now - 50*day + 1, "completed": now - 50*day + 2001},
	})
	insertMessage(t, d, "u-new", "s2", now-day, map[string]any{"role": "user"})
	insertMessage(t, d, "a-new", "s2", now-day+1, map[string]any{
		"role": "assistant", "agent": "explore", "providerID": "openai", "modelID": "gpt", "error": map[string]any{"name": "x"},
		"tokens": map[string]any{"input": 7, "output": 3},
		"time":   map[string]any{"created": now - day + 1, "completed": now - day + 501},
	})
}

type analyticsSnapshot struct {
	dash   [2]*MetricsDashboard
	models [2][]ModelUsage
	daily  []DailyActivity
	hourly []HourlyTokensByModel
	stats  *Stats
}

func takeAnalytics(t *testing.T, d *DB, now int64) analyticsSnapshot {
	t.Helper()
	ctx := t.Context()
	var s analyticsSnapshot
	var err error
	for i, since := range []int64{0, now - 7*24*3600*1000} {
		if s.dash[i], err = d.GetMetricsDashboard(ctx, MetricsDashboardOptions{Since: since, Dir: "/p"}); err != nil {
			t.Fatal(err)
		}
		if s.models[i], err = d.GetModelUsage(ctx, since, ""); err != nil {
			t.Fatal(err)
		}
	}
	if s.daily, err = d.GetDailyActivity(ctx, 0, "", ""); err != nil {
		t.Fatal(err)
	}
	if s.hourly, err = d.GetHourlyTokensByModel(ctx, 7, 0, "", ""); err != nil {
		t.Fatal(err)
	}
	if s.stats, err = d.GetStats(ctx); err != nil {
		t.Fatal(err)
	}
	return s
}

func openMirrored(t *testing.T) (*DB, string) {
	t.Helper()
	d := openTestDB(t)
	t.Cleanup(func() { d.Close() })
	d.db.SetMaxOpenConns(1) // one :memory: database, not one per connection
	path := filepath.Join(t.TempDir(), "analytics-cache.db")
	if err := d.EnableAnalyticsMirror(path, "src-a"); err != nil {
		t.Fatal(err)
	}
	return d, path
}

func mirrorIDs(t *testing.T, d *DB) []string {
	t.Helper()
	rows, err := d.mirror.db.Query(`SELECT id FROM message ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return ids
}

// forceStale makes the next analytics read sync first.
func forceStale(d *DB) { d.mirror.lastSync.Store(0) }

// TestAnalyticsMirrorParity: every analytics query returns exactly what it
// returns against OpenCode directly.
func TestAnalyticsMirrorParity(t *testing.T) {
	now := time.Now().UnixMilli()
	direct := openTestDB(t)
	defer direct.Close()
	seedMirrorSource(t, direct, now)
	want := takeAnalytics(t, direct, now)

	d, _ := openMirrored(t)
	seedMirrorSource(t, d, now)
	if err := d.SyncAnalyticsMirror(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := d.analytics(t.Context()); got != d.mirror.db {
		t.Fatal("analytics() did not return the built mirror")
	}
	if got := takeAnalytics(t, d, now); !reflect.DeepEqual(got, want) {
		t.Fatalf("mirror results differ from direct:\n got %+v\nwant %+v", got, want)
	}
	var userData string
	if err := d.mirror.db.QueryRow(`SELECT data FROM message WHERE id = 'u-old'`).Scan(&userData); err != nil {
		t.Fatal(err)
	}
	if userData != `{"role":"user"}` {
		t.Fatalf("user message data = %q, want it stripped to the role", userData)
	}
}

// TestAnalyticsMirrorIncremental covers what the incremental sync must pick
// up: new messages, an unfinished turn completing, a message reverted inside
// the sync window, and a deleted session's old messages. (Deletions older
// than the window wait for the periodic full rebuild.)
func TestAnalyticsMirrorIncremental(t *testing.T) {
	now := time.Now().UnixMilli()
	d, _ := openMirrored(t)
	seedMirrorSource(t, d, now)
	insertMessage(t, d, "a-running", "s2", now-30*60*1000, map[string]any{"role": "assistant", "modelID": "gpt"})
	insertMessage(t, d, "a-reverted", "s2", now-20*60*1000, map[string]any{"role": "assistant", "modelID": "gpt", "finish": "stop"})
	if err := d.SyncAnalyticsMirror(t.Context()); err != nil {
		t.Fatal(err)
	}

	insertMessage(t, d, "a-later", "s2", now, map[string]any{"role": "assistant", "modelID": "gpt"})
	if _, err := d.db.Exec(`UPDATE message SET data = '{"role":"assistant","modelID":"gpt","finish":"stop"}' WHERE id = 'a-running'`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.db.Exec(`DELETE FROM message WHERE id = 'a-reverted'; DELETE FROM session WHERE id = 's1'`); err != nil {
		t.Fatal(err)
	}
	forceStale(d)
	d.analytics(t.Context())

	if got, want := mirrorIDs(t, d), []string{"a-later", "a-new", "a-running", "u-new"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("mirror ids = %v, want %v", got, want)
	}
	var settled int
	if err := d.mirror.db.QueryRow(`SELECT settled FROM message WHERE id = 'a-running'`).Scan(&settled); err != nil {
		t.Fatal(err)
	}
	if settled != 1 {
		t.Fatal("completed turn was not re-copied")
	}
}

// TestAnalyticsMirrorBuildsInBackground: before the first build, analytics
// read OpenCode and a build starts; a reopened mirror is ready at once, and
// one from another source is discarded.
func TestAnalyticsMirrorBuildsInBackground(t *testing.T) {
	now := time.Now().UnixMilli()
	d, path := openMirrored(t)
	seedMirrorSource(t, d, now)
	if got := d.analytics(t.Context()); got != d.db {
		t.Fatal("unbuilt mirror should fall back to OpenCode")
	}
	deadline := time.Now().Add(5 * time.Second)
	for !d.mirror.ready.Load() || d.mirror.building.Load() {
		if time.Now().After(deadline) {
			t.Fatal("background build never finished")
		}
		time.Sleep(5 * time.Millisecond)
	}
	d.mirror.db.Close()

	if err := d.EnableAnalyticsMirror(path, "src-a"); err != nil {
		t.Fatal(err)
	}
	if !d.mirror.ready.Load() || len(mirrorIDs(t, d)) != 4 {
		t.Fatal("reopened mirror should be ready with its rows")
	}
	d.mirror.db.Close()

	if err := d.EnableAnalyticsMirror(path, "src-b"); err != nil {
		t.Fatal(err)
	}
	if d.mirror.ready.Load() || len(mirrorIDs(t, d)) != 0 {
		t.Fatal("mirror from another source should be wiped")
	}
}
