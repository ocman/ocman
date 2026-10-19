package server

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/conversationingest"
	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/statev2"
)

// On a mixed-platform startup the analytics mirror also carries Claude's
// rows. Parity compares the OpenCode history instance, so it must compare
// OpenCode's analytics alone rather than report Claude activity as drift.
func TestConversationHistoryParityIgnoresOtherPlatformsAnalytics(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.db")
	rw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	day := time.Now().AddDate(0, 0, -3).UnixMilli()
	for _, stmt := range []string{
		`CREATE TABLE session (id TEXT PRIMARY KEY, project_id TEXT NOT NULL DEFAULT '', parent_id TEXT, title TEXT NOT NULL DEFAULT '',
			directory TEXT NOT NULL DEFAULT '', time_created INTEGER NOT NULL DEFAULT 0, time_updated INTEGER NOT NULL DEFAULT 0,
			summary_additions INTEGER, summary_deletions INTEGER, summary_files INTEGER, share_url TEXT)`,
		`CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT NOT NULL, time_created INTEGER NOT NULL DEFAULT 0, data TEXT NOT NULL DEFAULT '{}')`,
		`CREATE TABLE part (id TEXT PRIMARY KEY, message_id TEXT NOT NULL, session_id TEXT NOT NULL, time_created INTEGER NOT NULL DEFAULT 0, data TEXT NOT NULL DEFAULT '{}')`,
		fmt.Sprintf(`INSERT INTO session (id, title, directory, time_created, time_updated) VALUES ('oc', 'OpenCode', '/repo', %d, %d)`, day, day),
		fmt.Sprintf(`INSERT INTO message (id, session_id, time_created, data) VALUES ('oc-u', 'oc', %d, '{"role":"user","time":{"created":%d}}')`, day, day),
		fmt.Sprintf(`INSERT INTO message (id, session_id, time_created, data) VALUES ('oc-a', 'oc', %d, '{"role":"assistant","finish":"stop","providerID":"p","modelID":"m","tokens":{"input":10,"output":5},"time":{"created":%d,"completed":%d}}')`, day+1, day+1, day+2),
	} {
		if _, err := rw.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	_ = rw.Close()
	source, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if err := source.EnableAnalyticsMirror(filepath.Join(t.TempDir(), "analytics-cache.db"), path); err != nil {
		t.Fatal(err)
	}
	source.AddAnalyticsSource(func(context.Context, int64, int64) ([]db.AnalyticsSession, []db.AnalyticsMessage, error) {
		return []db.AnalyticsSession{{ID: "claude", Title: "Claude", Directory: "/repo", TimeCreated: day}},
			[]db.AnalyticsMessage{
				{ID: "cl-u", SessionID: "claude", TimeCreated: day, Settled: true, Data: `{"role":"user"}`},
				{ID: "cl-a", SessionID: "claude", TimeCreated: day + 1, Settled: true, Data: `{"role":"assistant","providerID":"anthropic","modelID":"claude","tokens":{"input":7,"output":3}}`},
			}, nil
	})
	store, err := statev2.Open(filepath.Join(t.TempDir(), "statev2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	key, err := source.HistorySourceKey(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RegisterInstance(t.Context(), statev2.Instance{ID: "opencode", OwnerID: "owner", Platform: "opencode", SourceKey: key}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := source.HistorySnapshot(t.Context(), "oc")
	if err != nil {
		t.Fatal(err)
	}
	batch, _, err := conversationingest.Normalize(statev2.Key{InstanceID: "opencode", SessionID: "oc"}, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Commit(t.Context(), batch); err != nil {
		t.Fatal(err)
	}
	s := New(source, nil, "127.0.0.1:0", platforms.NewRegistry(), nil).WithConversationHistory(store, "owner")
	if s.historyParity == nil {
		t.Fatal("parity not registered")
	}
	report, err := s.historyParity.Compare(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if report.Days == 0 || report.Models == 0 || len(report.Mismatches) != 0 {
		t.Fatalf("false parity mismatch from another platform's rows: %+v", report)
	}
}
