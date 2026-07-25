package state

import (
	"fmt"
	"strings"
	"time"
)

// ProjectRootForDirectory folds a managed worktree path to its project root.
func ProjectRootForDirectory(directory string) string {
	if directory == "" {
		return directory
	}
	cleaned := directory
	if len(cleaned) > 1 && strings.HasSuffix(cleaned, "/") {
		cleaned = cleaned[:len(cleaned)-1]
	}
	parts := strings.Split(cleaned, "/")
	idx := -1
	for i, part := range parts {
		if part == ".worktrees" {
			idx = i
			break
		}
	}
	if idx <= 0 || len(parts) < idx+3 {
		return cleaned
	}
	prefix := strings.Join(parts[:idx], "/")
	if prefix == "" {
		return cleaned
	}
	return prefix + "/" + parts[idx+1]
}

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
	return nil
}

// UnarchiveSession removes a session's archived marker (per platform).
func (d *DB) UnarchiveSession(platform, sessionID string) error {
	_, err := d.db.Exec(
		`DELETE FROM archived_session WHERE platform = ? AND session_id = ?`,
		platform, sessionID,
	)
	if err != nil {
		return fmt.Errorf("unarchiving session: %w", err)
	}
	return nil
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
	return nil
}

// UnarchiveProject removes a project's archived marker.
func (d *DB) UnarchiveProject(projectRoot string) error {
	_, err := d.db.Exec(
		`DELETE FROM archived_project WHERE project_root = ?`,
		projectRoot,
	)
	if err != nil {
		return fmt.Errorf("unarchiving project: %w", err)
	}
	return nil
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
