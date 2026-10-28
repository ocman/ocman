// Package sqliteutil holds SQLite file and pool setup shared by ocman's
// database packages.
package sqliteutil

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ocman/ocman/internal/telemetry"
)

// SecurePaths creates the database directory and main file privately and
// rejects symlinked or non-regular paths. The directory is made 0700 and the
// main file plus any -wal, -shm and -journal sidecars 0600. label names the
// database in errors, for example "state".
func SecurePaths(path, label string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating %s directory: %w", label, err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("checking %s directory: %w", label, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("%s directory %q must be a real directory", label, dir)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("securing %s directory: %w", label, err)
	}

	for i, candidate := range []string{path, path + "-wal", path + "-shm", path + "-journal"} {
		info, err := os.Lstat(candidate)
		if os.IsNotExist(err) {
			if i != 0 {
				continue
			}
			file, createErr := os.OpenFile(candidate, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
			if createErr != nil {
				return fmt.Errorf("creating %s database: %w", label, createErr)
			}
			if closeErr := file.Close(); closeErr != nil {
				return fmt.Errorf("closing new %s database: %w", label, closeErr)
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("checking %s file %q: %w", label, candidate, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("%s file %q must be a regular file", label, candidate)
		}
		if err := os.Chmod(candidate, 0o600); err != nil {
			return fmt.Errorf("securing %s file %q: %w", label, candidate, err)
		}
	}
	return nil
}

// Pool bounds an instrumented connection pool. Zero durations keep
// database/sql's default of never expiring connections.
type Pool struct {
	MaxOpen, MaxIdle         int
	MaxLifetime, MaxIdleTime time.Duration
}

// OpenPool opens an instrumented SQLite handle, applies the pool bounds and
// pings it, closing the handle when the ping fails.
func OpenPool(dsn, name string, p Pool) (*sql.DB, error) {
	conn, err := telemetry.OpenSQL(dsn, name)
	if err != nil {
		return nil, err
	}
	conn.SetMaxOpenConns(p.MaxOpen)
	conn.SetMaxIdleConns(p.MaxIdle)
	conn.SetConnMaxLifetime(p.MaxLifetime)
	conn.SetConnMaxIdleTime(p.MaxIdleTime)
	if err := conn.Ping(); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}
