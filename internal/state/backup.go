package state

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	log "github.com/sirupsen/logrus"
)

// backupBeforeMigration snapshots the whole pending migration batch, which runs
// in one transaction. VACUUM INTO includes committed WAL data without copying
// live SQLite files. In-memory databases have no persistent state to protect.
func backupBeforeMigration(db *sql.DB, version int) error {
	var path string
	if err := db.QueryRow(`SELECT file FROM pragma_database_list WHERE name = 'main'`).Scan(&path); err != nil {
		return fmt.Errorf("finding database path: %w", err)
	}
	if path == "" {
		return nil
	}
	pattern := fmt.Sprintf("%s.backup-v%d-to-v%d-*.tmp", filepath.Base(path), version, latestSchemaVersion)
	file, err := os.CreateTemp(filepath.Dir(path), pattern)
	if err != nil {
		return fmt.Errorf("creating backup file: %w", err)
	}
	defer file.Close()
	defer os.Remove(file.Name())
	// CreateTemp reserves a unique, owner-only file. SQLite accepts an empty
	// destination; binding its name also handles quotes in the database path.
	if _, err := db.Exec(`VACUUM main INTO ?`, file.Name()); err != nil {
		return fmt.Errorf("snapshotting database: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("syncing backup: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("closing backup: %w", err)
	}
	backupPath := strings.TrimSuffix(file.Name(), ".tmp") + ".db"
	if err := os.Rename(file.Name(), backupPath); err != nil {
		return fmt.Errorf("publishing backup: %w", err)
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return fmt.Errorf("opening backup directory: %w", err)
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil {
		return fmt.Errorf("syncing backup directory: %w", err)
	}
	log.WithFields(log.Fields{
		"path": backupPath, "from_version": version, "to_version": latestSchemaVersion,
	}).Info("state: saved pre-migration backup")
	return nil
}
