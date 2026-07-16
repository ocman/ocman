package state

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"github.com/XSAM/otelsql"
	"go.opentelemetry.io/otel/attribute"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	_ "modernc.org/sqlite"
)

func nullableInt(value int64) any {
	if value == 0 {
		return nil
	}
	return value
}

// DB wraps the writable ocman state database. Methods are grouped by
// concern in sibling files (seen.go, archive.go, autoapprove.go,
// childsessions.go, favorites.go, pins.go, settings.go, auth.go,
// sharelinks.go, remote.go, identity.go); this file owns
// the connection lifecycle and shared helpers.
type DB struct {
	db *sql.DB
}

// DefaultDBPath returns the default path to the ocman state database.
// Falls back to a relative path if the home directory cannot be determined.
func DefaultDBPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".local", "share", "ocman", "state.db")
	}
	return filepath.Join(home, ".local", "share", "ocman", "state.db")
}

// DefaultDataDir returns the ocman data directory (where state.db and
// content-addressed artifact payloads live). Mirrors DefaultDBPath.
func DefaultDataDir() string {
	return filepath.Dir(DefaultDBPath())
}

// Open opens the ocman state database and ensures the schema exists.
// Runs any pending migrations exactly once; safe to call on every boot.
func Open(path string) (*DB, error) {
	if err := secureStatePaths(path); err != nil {
		return nil, err
	}

	dsn := fmt.Sprintf("file:%s?_busy_timeout=5000&_journal_mode=WAL", path)
	// See internal/db.Open for the otelsql rationale; same trade-off
	// applies. db.name="ocman" distinguishes ocman's own state from
	// the upstream OpenCode database in trace and metric attributes.
	db, err := otelsql.Open("sqlite", dsn,
		otelsql.WithAttributes(
			semconv.DBSystemSqlite,
			attribute.String("db.name", "ocman"),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("opening state database: %w", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("pinging state database: %w", err)
	}
	// Serialize all access on a single connection. The state DB is
	// written concurrently (HTTP handlers + the background remote dial
	// goroutine), and SQLite allows only one writer at a time — with a
	// multi-connection pool that races into SQLITE_BUSY despite WAL +
	// busy_timeout. One connection is plenty for a single-user dashboard.
	db.SetMaxOpenConns(1)

	stateDB := &DB{db: db}
	if err := stateDB.init(); err != nil {
		db.Close()
		return nil, err
	}
	if err := secureStatePaths(path); err != nil {
		db.Close()
		return nil, err
	}
	_, _ = otelsql.RegisterDBStatsMetrics(db,
		otelsql.WithAttributes(
			semconv.DBSystemSqlite,
			attribute.String("db.name", "ocman"),
		),
	)

	return stateDB, nil
}

func secureStatePaths(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating state directory: %w", err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("checking state directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("state directory %q must be a real directory", dir)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("securing state directory: %w", err)
	}

	for i, candidate := range []string{path, path + "-wal", path + "-shm", path + "-journal"} {
		info, err := os.Lstat(candidate)
		if os.IsNotExist(err) {
			if i != 0 {
				continue
			}
			file, createErr := os.OpenFile(candidate, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
			if createErr != nil {
				return fmt.Errorf("creating state database: %w", createErr)
			}
			if closeErr := file.Close(); closeErr != nil {
				return fmt.Errorf("closing new state database: %w", closeErr)
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("checking state file %q: %w", candidate, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("state file %q must be a regular file", candidate)
		}
		if err := os.Chmod(candidate, 0o600); err != nil {
			return fmt.Errorf("securing state file %q: %w", candidate, err)
		}
	}
	return nil
}

// OpenFromSQL wraps an existing *sql.DB in a state.DB and runs
// migrations. Intended for tests that open an in-memory database
// directly rather than via Open (which also handles directory creation
// and OTel instrumentation).
func OpenFromSQL(sqlDB *sql.DB) (*DB, error) {
	d := &DB{db: sqlDB}
	if err := d.init(); err != nil {
		return nil, err
	}
	return d, nil
}

// init bootstraps the schema to latestSchemaVersion. See migrate.go
// for the migration plan.
func (d *DB) init() error {
	return migrate(d.db)
}

// Close closes the state database.
func (d *DB) Close() error {
	return d.db.Close()
}

// Key identifies a session by its owning platform + session ID. Used
// as the map key for state lookups so two different platforms can
// share a session-id namespace without colliding.
type Key struct {
	Platform  string
	SessionID string
}

// nullableString converts an empty string to a nil interface (stored as
// NULL in SQLite) and a non-empty string to itself. This keeps nullable
// TEXT columns clean rather than storing empty strings.
func nullableString(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}
