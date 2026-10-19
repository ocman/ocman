package statev2

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"github.com/NoUseFreak/ocman/internal/telemetry"
	log "github.com/sirupsen/logrus"
	_ "modernc.org/sqlite"
)

// DB owns a separate statev2.db. Callers supply the path explicitly so tests
// and development never silently fall back to a live or another checkout's DB.
type DB struct {
	db   *sql.DB
	path string
}

// Path places statev2.db beside an explicitly resolved legacy state database.
func Path(statePath string) string {
	return filepath.Join(filepath.Dir(statePath), "statev2.db")
}

func Open(path string) (*DB, error) {
	if path == "" || path == ":memory:" {
		return nil, fmt.Errorf("statev2: an explicit database file path is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err := securePaths(abs); err != nil {
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
	if err := securePaths(abs); err != nil {
		conn.Close()
		return nil, err
	}
	return d, nil
}

func (d *DB) Close() error { return d.db.Close() }

// FilePath identifies this store for independently owned, read-only readers.
func (d *DB) FilePath() string { return d.path }

func securePaths(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	dir, err := os.Lstat(filepath.Dir(path))
	if err != nil {
		return err
	}
	if !dir.IsDir() {
		return fmt.Errorf("statev2: database directory must not be a symlink")
	}
	for i, candidate := range []string{path, path + "-wal", path + "-shm", path + "-journal"} {
		info, err := os.Lstat(candidate)
		if os.IsNotExist(err) {
			if i != 0 {
				continue
			}
			f, err := os.OpenFile(candidate, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
			if err != nil {
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("statev2: %s must be a regular file, not a symlink", candidate)
		}
		if err := os.Chmod(candidate, 0600); err != nil {
			return err
		}
	}
	return nil
}

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
		log.Info("statev2: building analytics summaries from retained compact projections")
		if err := backfillAnalyticsSummaries(context.Background(), tx); err != nil {
			return fmt.Errorf("backfilling analytics summaries: %w", err)
		}
	}
	if _, err := tx.Exec(fmt.Sprintf("PRAGMA application_id=%d; PRAGMA user_version=%d", applicationID, len(migrations))); err != nil {
		return err
	}
	return tx.Commit()
}
