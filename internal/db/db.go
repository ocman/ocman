package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/XSAM/otelsql"
	_ "github.com/mattn/go-sqlite3"
	"go.opentelemetry.io/otel/attribute"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// DB wraps the SQLite connection.
type DB struct {
	db *sql.DB
}

// Connection-pool tuning constants for the read-only handle.
//
// Why a small bounded pool? OpenCode owns the database; ocman only
// reads from it. database/sql's default behaviour is "open as many
// connections as concurrent callers ask for", which under the
// dashboard's polling load can produce a lot of long-running
// transactions in WAL mode. Each transaction pins the read pointer
// and prevents the WAL from being checkpointed back into the main
// database file — the WAL grows, OpenCode's own queries get slower,
// and on macOS the resulting mmap pressure can stall the parent
// process for seconds at a time.
//
// 4 is plenty for ocman's read-bursty workload: a typical request
// runs 1–3 queries sequentially per goroutine, and request
// concurrency on a localhost-only dashboard rarely exceeds a handful.
// If `db.Stats().WaitCount` ever climbs in production we can bump
// this; it's instrumented in /api/system/stats.
const (
	maxOpenReadConns = 4
	maxIdleReadConns = 2

	// connMaxLifetime forces handles to be recycled even when idle
	// stays warm. Recycling drops the underlying SQLite connection
	// and its mmap region, providing a periodic safety valve in
	// case something downstream leaks a transaction.
	connMaxLifetime = 5 * time.Minute

	// connMaxIdleTime closes idle handles between dashboard polls
	// when the user steps away. Without this, two SQLite connections
	// would stay open forever holding file handles and mmap regions.
	connMaxIdleTime = 1 * time.Minute
)

// DefaultDBPath returns the default path to the OpenCode database.
// Falls back to a relative path if the home directory cannot be determined.
func DefaultDBPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".local", "share", "opencode", "opencode.db")
	}
	return filepath.Join(home, ".local", "share", "opencode", "opencode.db")
}

// Open opens the OpenCode database in read-only mode with a bounded
// connection pool.
//
// DSN flags:
//   - mode=ro          opens the file read-only at the OS level.
//   - _journal_mode=WAL keeps WAL semantics so we don't fight
//     OpenCode's writer.
//   - _query_only=1    SQLite-level "this connection cannot write",
//     defense-in-depth on top of mode=ro.
//   - _busy_timeout=5000 wait politely for up to 5 s if a checkpoint
//     or backup briefly contends with us, instead of failing
//     SQLITE_BUSY straight away.
//
// The pool is capped (see maxOpenReadConns / maxIdleReadConns) so
// ocman can't accidentally hold dozens of long-running read
// transactions, which would prevent OpenCode from checkpointing the
// WAL.
func Open(path string) (*DB, error) {
	dsn := fmt.Sprintf("file:%s?mode=ro&_journal_mode=WAL&_query_only=1&_busy_timeout=5000", path)
	// otelsql.Open wraps the underlying sqlite3 driver so every
	// database/sql operation produces a span and increments the
	// standard db.client.* metrics. When telemetry is disabled the
	// global TracerProvider/MeterProvider are no-ops, so this adds
	// only the minimal driver-wrapper overhead (a few nanoseconds
	// per call).
	db, err := otelsql.Open("sqlite3", dsn,
		otelsql.WithAttributes(
			semconv.DBSystemSqlite,
			attribute.String("db.name", "opencode"),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}
	db.SetMaxOpenConns(maxOpenReadConns)
	db.SetMaxIdleConns(maxIdleReadConns)
	db.SetConnMaxLifetime(connMaxLifetime)
	db.SetConnMaxIdleTime(connMaxIdleTime)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("pinging database: %w", err)
	}
	// Best-effort: register pool stats as OTel metrics. The error is
	// only meaningful when telemetry is wired up; ignoring it here
	// keeps the no-op path silent.
	_, _ = otelsql.RegisterDBStatsMetrics(db,
		otelsql.WithAttributes(
			semconv.DBSystemSqlite,
			attribute.String("db.name", "opencode"),
		),
	)
	return &DB{db: db}, nil
}

// OpenReadWrite opens the database in read-write mode. This is intended for
// test setup where schema creation must happen before read-only access.
func OpenReadWrite(path string) (*DB, error) {
	dsn := fmt.Sprintf("file:%s?_journal_mode=WAL", path)
	db, err := otelsql.Open("sqlite3", dsn,
		otelsql.WithAttributes(
			semconv.DBSystemSqlite,
			attribute.String("db.name", "opencode-rw"),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("pinging database: %w", err)
	}
	return &DB{db: db}, nil
}

// Close closes the database.
func (d *DB) Close() error {
	return d.db.Close()
}

// Stats returns the underlying database/sql connection-pool stats.
// Surfaced via /api/system/stats so an operator can see at a glance
// whether ocman is throttling on the pool cap (WaitCount climbing)
// or holding stale handles (Idle close to MaxOpen for long periods).
//
// Returning the stdlib type directly avoids re-modelling fields the
// caller would just unmarshal back into the same shape.
func (d *DB) Stats() sql.DBStats {
	return d.db.Stats()
}
