package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/statev2"
)

// checkHistoryScope opens and releases one scoped reader.
func (d *DB) checkHistoryScope(ctx context.Context) error {
	_, release, err := d.ScopedHistoryReader(ctx)
	if err == nil {
		release()
	}
	return err
}

func TestHistoryAnalyticsScopesSourcesAndRejectsCollisions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history ?# ü.db")
	store, err := statev2.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, id := range []string{"one", "two"} {
		if err := store.RegisterInstance(t.Context(), statev2.Instance{ID: id, OwnerID: "o", Platform: "p", SourceKey: id}); err != nil {
			t.Fatal(err)
		}
		_, err := store.Commit(t.Context(), statev2.Batch{Session: statev2.Session{Key: statev2.Key{InstanceID: id, SessionID: "same"}, Title: "Root"}, Messages: []statev2.Message{{ID: "same-message", Role: "user", Extensions: json.RawMessage(`{"role":"user","attachment":"excluded"}`)}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	single, err := OpenHistoryAnalytics(path, []string{"one"})
	if err != nil {
		t.Fatal(err)
	}
	defer single.Close()
	if _, err := single.db.Exec(`DELETE FROM main.analytics_message`); err == nil {
		t.Fatal("history reader could write")
	}
	if err := single.checkHistoryScope(t.Context()); err != nil {
		t.Fatal(err)
	}
	stats, err := single.GetStats(t.Context())
	if err != nil || stats.TotalSessions != 1 || stats.TotalMessages != 1 {
		t.Fatalf("scope: %+v %v", stats, err)
	}
	both, err := OpenHistoryAnalytics(path, []string{"one", "two"})
	if err != nil {
		t.Fatal(err)
	}
	defer both.Close()
	if err := both.checkHistoryScope(t.Context()); err == nil {
		t.Fatal("native ID collision not rejected")
	}
	// Every new pooled connection must install its own scoped TEMP views.
	errs := make(chan error, 12)
	for range 12 {
		go func() { _, err := single.GetStats(t.Context()); errs <- err }()
	}
	for range 12 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := single.checkHistoryScope(ctx); err == nil {
		t.Fatal("ignored cancellation")
	}
}

func TestHistoryAnalyticsOpenErrors(t *testing.T) {
	for _, tc := range []struct {
		path string
		ids  []string
	}{{"", []string{"one"}}, {"missing", nil}, {filepath.Join(t.TempDir(), "missing.db"), []string{"one"}}} {
		if reader, err := OpenHistoryAnalytics(tc.path, tc.ids); err == nil {
			reader.Close()
			t.Fatal("opened invalid history")
		}
	}
	// A foreign SQLite schema cannot be interpreted as conversation history.
	path := filepath.Join(t.TempDir(), "foreign.db")
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`CREATE TABLE foreign_data(value TEXT)`); err != nil {
		t.Fatal(err)
	}
	conn.Close()
	reader, err := OpenHistoryAnalytics(path, []string{"one"})
	if err == nil {
		defer reader.Close()
		if err := reader.checkHistoryScope(t.Context()); err == nil {
			t.Fatal("accepted foreign database")
		}
	}
}
