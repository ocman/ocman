package state

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// DB wraps the writable ocman state database.
type DB struct {
	db *sql.DB
}

// DefaultDBPath returns the default path to the ocman state database.
func DefaultDBPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "ocman", "state.db")
}

// Open opens the ocman state database and ensures the schema exists.
func Open(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("creating state directory: %w", err)
	}

	dsn := fmt.Sprintf("file:%s?_busy_timeout=5000&_journal_mode=WAL", path)
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening state database: %w", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("pinging state database: %w", err)
	}

	stateDB := &DB{db: db}
	if err := stateDB.init(); err != nil {
		db.Close()
		return nil, err
	}

	return stateDB, nil
}

func (d *DB) init() error {
	_, err := d.db.Exec(`
		CREATE TABLE IF NOT EXISTS archived_session (
			session_id TEXT PRIMARY KEY,
			session_time_updated INTEGER NOT NULL,
			archived_at INTEGER NOT NULL
		);

		CREATE TABLE IF NOT EXISTS seen_session (
			session_id TEXT PRIMARY KEY,
			session_time_updated INTEGER NOT NULL,
			seen_at INTEGER NOT NULL
		)
	`)
	if err != nil {
		return fmt.Errorf("initializing state schema: %w", err)
	}
	return nil
}

// MarkSessionSeen records the latest session update the user has viewed.
func (d *DB) MarkSessionSeen(sessionID string, sessionTimeUpdated int64) error {
	_, err := d.db.Exec(`
		INSERT INTO seen_session (session_id, session_time_updated, seen_at)
		VALUES (?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET
			session_time_updated = CASE
				WHEN excluded.session_time_updated > seen_session.session_time_updated THEN excluded.session_time_updated
				ELSE seen_session.session_time_updated
			END,
			seen_at = excluded.seen_at
	`, sessionID, sessionTimeUpdated, time.Now().UnixMilli())
	if err != nil {
		return fmt.Errorf("marking session seen: %w", err)
	}
	return nil
}

// SeenSessions returns seen session timestamps keyed by session ID.
func (d *DB) SeenSessions() (map[string]int64, error) {
	rows, err := d.db.Query(`SELECT session_id, session_time_updated FROM seen_session`)
	if err != nil {
		return nil, fmt.Errorf("listing seen sessions: %w", err)
	}
	defer rows.Close()

	seen := make(map[string]int64)
	for rows.Next() {
		var sessionID string
		var sessionTimeUpdated int64
		if err := rows.Scan(&sessionID, &sessionTimeUpdated); err != nil {
			return nil, fmt.Errorf("scanning seen session: %w", err)
		}
		seen[sessionID] = sessionTimeUpdated
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading seen sessions: %w", err)
	}

	return seen, nil
}

// Close closes the state database.
func (d *DB) Close() error {
	return d.db.Close()
}

// ArchiveSession records a session as archived at its current update timestamp.
func (d *DB) ArchiveSession(sessionID string, sessionTimeUpdated int64) error {
	_, err := d.db.Exec(`
		INSERT INTO archived_session (session_id, session_time_updated, archived_at)
		VALUES (?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET
			session_time_updated = excluded.session_time_updated,
			archived_at = excluded.archived_at
	`, sessionID, sessionTimeUpdated, time.Now().UnixMilli())
	if err != nil {
		return fmt.Errorf("archiving session: %w", err)
	}
	return nil
}

// UnarchiveSession removes a session's archived marker.
func (d *DB) UnarchiveSession(sessionID string) error {
	_, err := d.db.Exec(`DELETE FROM archived_session WHERE session_id = ?`, sessionID)
	if err != nil {
		return fmt.Errorf("unarchiving session: %w", err)
	}
	return nil
}

// ArchivedSessions returns archived session timestamps keyed by session ID.
func (d *DB) ArchivedSessions() (map[string]int64, error) {
	rows, err := d.db.Query(`SELECT session_id, session_time_updated FROM archived_session`)
	if err != nil {
		return nil, fmt.Errorf("listing archived sessions: %w", err)
	}
	defer rows.Close()

	archived := make(map[string]int64)
	for rows.Next() {
		var sessionID string
		var sessionTimeUpdated int64
		if err := rows.Scan(&sessionID, &sessionTimeUpdated); err != nil {
			return nil, fmt.Errorf("scanning archived session: %w", err)
		}
		archived[sessionID] = sessionTimeUpdated
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading archived sessions: %w", err)
	}

	return archived, nil
}
