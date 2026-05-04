package db

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// makeTempDB seeds a temporary SQLite file with the minimal OpenCode
// schema and returns its absolute path.
//
// We seed with _journal_mode=WAL so the file already has WAL semantics
// before the test calls Open(). Without this, Open's read-only DSN
// fails on Ping(): the driver runs PRAGMA journal_mode=WAL itself,
// which is a write op and rejected by mode=ro on a fresh file.
// (Production never hits this because OpenCode is the writer and has
// already initialised the WAL by the time ocman opens its handle.)
func makeTempDB(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")

	rw, err := sql.Open("sqlite", "file:"+path+"?_journal_mode=WAL")
	if err != nil {
		t.Fatalf("seed open: %v", err)
	}
	defer rw.Close()
	if _, err := rw.Exec(`
		CREATE TABLE session (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL DEFAULT '',
			parent_id TEXT,
			title TEXT NOT NULL DEFAULT '',
			directory TEXT NOT NULL DEFAULT '',
			time_created INTEGER NOT NULL DEFAULT 0,
			time_updated INTEGER NOT NULL DEFAULT 0,
			summary_additions INTEGER,
			summary_deletions INTEGER,
			summary_files INTEGER,
			share_url TEXT
		);
		CREATE TABLE message (
			id TEXT PRIMARY KEY, session_id TEXT NOT NULL,
			time_created INTEGER NOT NULL DEFAULT 0,
			data TEXT NOT NULL DEFAULT '{}'
		);
		CREATE TABLE part (
			id TEXT PRIMARY KEY, message_id TEXT NOT NULL,
			session_id TEXT NOT NULL,
			time_created INTEGER NOT NULL DEFAULT 0,
			data TEXT NOT NULL DEFAULT '{}'
		);
	`); err != nil {
		t.Fatalf("seed schema: %v", err)
	}
	return path
}

// TestOpen_ConfiguresConnectionPool verifies that Open returns a
// database with a bounded pool. The configured cap is enforced by
// firing more concurrent queries than the cap and observing that
// at least one of them ends up in WaitCount > 0.
//
// This is the behavioural test of the "ocman shouldn't stockpile
// SQLite connections" contract — without this cap, the previous
// implementation would let database/sql open arbitrarily many
// connections under load, each holding a file handle and an mmap
// region, contributing to the WAL-checkpoint contention we
// hypothesised was hurting OpenCode.
func TestOpen_ConfiguresConnectionPool(t *testing.T) {
	path := makeTempDB(t)

	d, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()

	// Fire more concurrent queries than the pool cap. Each query
	// holds a connection while it sleeps in SQLite (we use a small
	// busy-loop CTE so the work is real, not just sleep).
	const concurrency = maxOpenReadConns + 4
	var wg sync.WaitGroup
	wg.Add(concurrency)
	for i := 0; i < concurrency; i++ {
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			rows, err := d.db.QueryContext(ctx, `
				WITH RECURSIVE c(x) AS (
					SELECT 1 UNION ALL SELECT x+1 FROM c WHERE x < 50000
				)
				SELECT COUNT(*) FROM c
			`)
			if err != nil {
				return
			}
			defer rows.Close()
			for rows.Next() {
				var n int
				_ = rows.Scan(&n)
			}
		}()
	}
	wg.Wait()

	stats := d.db.Stats()
	if stats.MaxOpenConnections != maxOpenReadConns {
		t.Errorf("MaxOpenConnections = %d, want %d", stats.MaxOpenConnections, maxOpenReadConns)
	}
	if stats.WaitCount == 0 {
		t.Errorf("WaitCount = 0; expected at least one query to wait on the pool cap (concurrency=%d, cap=%d)",
			concurrency, maxOpenReadConns)
	}
}

// TestOpen_AppliesQueryOnlyPragma confirms the _query_only=1 DSN
// flag took effect. Any attempt to write through this connection
// must fail with a runtime error (not a Go-side rejection — the
// flag is enforced inside SQLite, defense-in-depth on top of mode=ro).
func TestOpen_AppliesQueryOnlyPragma(t *testing.T) {
	path := makeTempDB(t)

	d, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()

	_, err = d.db.Exec(`INSERT INTO session (id) VALUES ('shouldnt-write')`)
	if err == nil {
		t.Fatal("expected write to fail under _query_only / mode=ro, got nil error")
	}
	// We don't assert the exact error message — the sqlite driver phrases
	// it differently for read-only mode vs query_only mode. Either
	// flavour is fine; the contract is "it must not succeed".
}

// TestOpen_AppliesBusyTimeout is a smoke test that the busy-timeout
// DSN option is recognised by the driver. We can't easily contrive
// a real busy condition in a unit test (it requires a writer holding
// a lock the reader wants), so this just verifies Open() doesn't
// error out parsing the DSN.
func TestOpen_AppliesBusyTimeout(t *testing.T) {
	path := makeTempDB(t)

	d, err := Open(path)
	if err != nil {
		t.Fatalf("Open with busy_timeout DSN: %v", err)
	}
	defer d.Close()

	if _, err := os.Stat(path); err != nil {
		t.Errorf("DB file missing post-Open: %v", err)
	}
}
