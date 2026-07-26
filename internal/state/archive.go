package state

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ArchiveSession records a session as archived at its current update
// timestamp for the given platform.
func (d *DB) ArchiveSession(platform, sessionID string, sessionTimeUpdated int64) error {
	_, err := d.db.Exec(`
		INSERT INTO archived_session (platform, session_id, session_time_updated, archived_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(platform, session_id) DO UPDATE SET
			session_time_updated = excluded.session_time_updated,
			archived_at = excluded.archived_at
	`, platform, sessionID, sessionTimeUpdated, time.Now().UnixMilli())
	if err != nil {
		return fmt.Errorf("archiving session: %w", err)
	}
	return d.clearUnarchive(unarchiveKindSession, sessionKey(platform, sessionID))
}

// UnarchiveSession removes a session's archived marker (per platform)
// and records the user's intent so auto-archive does not immediately
// re-hide it.
func (d *DB) UnarchiveSession(platform, sessionID string) error {
	_, err := d.db.Exec(
		`DELETE FROM archived_session WHERE platform = ? AND session_id = ?`,
		platform, sessionID,
	)
	if err != nil {
		return fmt.Errorf("unarchiving session: %w", err)
	}
	return d.recordUnarchive(unarchiveKindSession, sessionKey(platform, sessionID))
}

const (
	unarchiveKindSession = "session"
	unarchiveKindProject = "project"
)

// sessionKey packs a (platform, session) pair into one unarchive key.
// Platform ids never contain a NUL, so the pair round-trips unambiguously.
func sessionKey(platform, sessionID string) string {
	return platform + "\x00" + sessionID
}

// recordUnarchive stamps when the user last brought an entity back.
func (d *DB) recordUnarchive(kind, key string) error {
	_, err := d.db.Exec(`
		INSERT INTO unarchived_entity (kind, entity_key, unarchived_at)
		VALUES (?, ?, ?)
		ON CONFLICT(kind, entity_key) DO UPDATE SET
			unarchived_at = excluded.unarchived_at
	`, kind, key, time.Now().UnixMilli())
	if err != nil {
		return fmt.Errorf("recording unarchive: %w", err)
	}
	return nil
}

// clearUnarchive drops the intent marker. Called when the entity is
// archived again: the newer archive supersedes the older unarchive.
func (d *DB) clearUnarchive(kind, key string) error {
	_, err := d.db.Exec(`DELETE FROM unarchived_entity WHERE kind = ? AND entity_key = ?`, kind, key)
	if err != nil {
		return fmt.Errorf("clearing unarchive: %w", err)
	}
	return nil
}

// SessionsUnarchivedSince returns the sessions the user unarchived at or
// after cutoff. Auto-archive skips them so a deliberate unarchive is not
// undone by the same inactivity that archived the session originally.
func (d *DB) SessionsUnarchivedSince(cutoff int64) (map[Key]bool, error) {
	keys, err := d.unarchivedSince(unarchiveKindSession, cutoff)
	if err != nil {
		return nil, err
	}
	out := make(map[Key]bool, len(keys))
	for key := range keys {
		platform, sessionID, found := strings.Cut(key, "\x00")
		if !found {
			continue
		}
		out[Key{Platform: platform, SessionID: sessionID}] = true
	}
	return out, nil
}

// ProjectsUnarchivedSince returns the project roots the user unarchived
// at or after cutoff.
func (d *DB) ProjectsUnarchivedSince(cutoff int64) (map[string]bool, error) {
	return d.unarchivedSince(unarchiveKindProject, cutoff)
}

func (d *DB) unarchivedSince(kind string, cutoff int64) (map[string]bool, error) {
	rows, err := d.db.Query(
		`SELECT entity_key FROM unarchived_entity WHERE kind = ? AND unarchived_at >= ?`,
		kind, cutoff,
	)
	if err != nil {
		return nil, fmt.Errorf("listing unarchived entities: %w", err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, fmt.Errorf("scanning unarchived entity: %w", err)
		}
		out[key] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading unarchived entities: %w", err)
	}
	return out, nil
}

// IsSessionArchived reports whether a single session currently carries
// an archive marker.
func (d *DB) IsSessionArchived(platform, sessionID string) (bool, error) {
	var one int
	err := d.db.QueryRow(
		`SELECT 1 FROM archived_session WHERE platform = ? AND session_id = ?`,
		platform, sessionID,
	).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("checking archived session: %w", err)
	}
	return true, nil
}

// ArchivedSessions returns every archived session's time_updated,
// keyed by (platform, session-id).
func (d *DB) ArchivedSessions() (map[Key]int64, error) {
	rows, err := d.db.Query(`SELECT platform, session_id, session_time_updated FROM archived_session`)
	if err != nil {
		return nil, fmt.Errorf("listing archived sessions: %w", err)
	}
	defer rows.Close()

	archived := make(map[Key]int64)
	for rows.Next() {
		var platform, sessionID string
		var sessionTimeUpdated int64
		if err := rows.Scan(&platform, &sessionID, &sessionTimeUpdated); err != nil {
			return nil, fmt.Errorf("scanning archived session: %w", err)
		}
		archived[Key{Platform: platform, SessionID: sessionID}] = sessionTimeUpdated
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading archived sessions: %w", err)
	}

	return archived, nil
}

// ArchiveProject records a project (keyed by its folded project-root
// directory) as archived at the current time. Re-archiving refreshes
// archived_at so future session activity can auto-unarchive it.
func (d *DB) ArchiveProject(projectRoot string) error {
	_, err := d.db.Exec(`
		INSERT INTO archived_project (project_root, archived_at)
		VALUES (?, ?)
		ON CONFLICT(project_root) DO UPDATE SET
			archived_at = excluded.archived_at
	`, projectRoot, time.Now().UnixMilli())
	if err != nil {
		return fmt.Errorf("archiving project: %w", err)
	}
	return d.clearUnarchive(unarchiveKindProject, projectRoot)
}

// UnarchiveProject removes a project's archived marker and records the
// user's intent so auto-archive does not immediately re-hide it.
func (d *DB) UnarchiveProject(projectRoot string) error {
	_, err := d.db.Exec(
		`DELETE FROM archived_project WHERE project_root = ?`,
		projectRoot,
	)
	if err != nil {
		return fmt.Errorf("unarchiving project: %w", err)
	}
	return d.recordUnarchive(unarchiveKindProject, projectRoot)
}

// ArchivedProjects returns every archived project's archived_at time,
// keyed by the folded project-root directory.
func (d *DB) ArchivedProjects() (map[string]int64, error) {
	rows, err := d.db.Query(`SELECT project_root, archived_at FROM archived_project`)
	if err != nil {
		return nil, fmt.Errorf("listing archived projects: %w", err)
	}
	defer rows.Close()

	archived := make(map[string]int64)
	for rows.Next() {
		var root string
		var archivedAt int64
		if err := rows.Scan(&root, &archivedAt); err != nil {
			return nil, fmt.Errorf("scanning archived project: %w", err)
		}
		archived[root] = archivedAt
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading archived projects: %w", err)
	}

	return archived, nil
}
