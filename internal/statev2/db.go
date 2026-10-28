package statev2

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"sync/atomic"

	"github.com/ocman/ocman/internal/sqliteutil"
	"github.com/ocman/ocman/internal/telemetry"
	log "github.com/sirupsen/logrus"
	_ "modernc.org/sqlite"
)

// DB owns a separate state_conv.db. Callers supply the path explicitly so tests
// and development never silently fall back to a live or another checkout's DB.
type DB struct {
	db        *sql.DB
	read      *sql.DB
	path      string
	committed atomic.Pointer[CommitObserver]
}

// Path places state_conv.db beside an explicitly resolved application database.
func Path(statePath string) string {
	return filepath.Join(filepath.Dir(statePath), "state_conv.db")
}

func Open(path string) (*DB, error) {
	if path == "" || path == ":memory:" {
		return nil, fmt.Errorf("statev2: an explicit database file path is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err := sqliteutil.SecurePaths(abs, "statev2"); err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: abs}
	q := url.Values{"_pragma": {"busy_timeout(5000)", "journal_mode(WAL)", "foreign_keys(1)", "synchronous(FULL)"}}
	u.RawQuery = q.Encode()
	conn, err := telemetry.OpenSQL(u.String(), "statev2")
	if err != nil {
		return nil, err
	}
	conn.SetMaxOpenConns(1)
	d := &DB{db: conn, path: abs}
	if err := d.migrate(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("opening statev2: %w", err)
	}
	if err := sqliteutil.SecurePaths(abs, "statev2"); err != nil {
		conn.Close()
		return nil, err
	}
	u.RawQuery = url.Values{"mode": {"ro"}, "_pragma": {"busy_timeout(1000)", "foreign_keys(1)", "query_only(1)"}}.Encode()
	read, err := sqliteutil.OpenPool(u.String(), "statev2-read", sqliteutil.Pool{MaxOpen: 4, MaxIdle: 4})
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	d.read = read
	return d, nil
}

func (d *DB) Close() error {
	var readErr error
	if d.read != nil {
		readErr = d.read.Close()
	}
	return errors.Join(readErr, d.db.Close())
}

// Interactive readers must not wait for the writer's database/sql connection.
// The read-only pool observes committed WAL snapshots, never partial imports.
func (d *DB) reader() *sql.DB {
	if d.read != nil {
		return d.read
	}
	return d.db
}

// FilePath identifies this store for independently owned, read-only readers.
func (d *DB) FilePath() string { return d.path }

func (d *DB) migrate() error {
	tx, err := d.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var version, appID int
	if err := tx.QueryRow("PRAGMA application_id").Scan(&appID); err != nil {
		return err
	}
	// OCv2 identifies this file and prevents opening legacy or foreign data.
	const applicationID = 0x4f437632
	if err := tx.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if appID != 0 && appID != applicationID {
		return fmt.Errorf("unexpected database application ID %d", appID)
	}
	if appID == 0 {
		var count int
		if err := tx.QueryRow("SELECT count(*) FROM sqlite_master WHERE name NOT LIKE 'sqlite_%'").Scan(&count); err != nil {
			return err
		}
		if count != 0 || version != 0 {
			return fmt.Errorf("refusing to initialize a nonempty foreign database")
		}
	}
	if version < 0 {
		return fmt.Errorf("invalid schema version %d", version)
	}
	if appID == applicationID && (version < 5 || version > len(migrations)) {
		return fmt.Errorf("unsupported conversation history schema v%d (supported: v5..v%d); refusing to reset stored history", version, len(migrations))
	}
	for i := version; i < len(migrations); i++ {
		if _, err := tx.Exec(migrations[i]); err != nil {
			return fmt.Errorf("migration v%d: %w", i+1, err)
		}
	}
	if version < 6 {
		log.Info("conversations: building analytics summaries from retained compact projections")
		if err := backfillAnalyticsSummaries(context.Background(), tx); err != nil {
			return fmt.Errorf("backfilling analytics summaries: %w", err)
		}
	}
	if _, err := tx.Exec(fmt.Sprintf("PRAGMA application_id=%d; PRAGMA user_version=%d", applicationID, len(migrations))); err != nil {
		return err
	}
	return tx.Commit()
}
