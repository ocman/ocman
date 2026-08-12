package state

import (
	"database/sql"
	"errors"
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
	return d.clearUnarchive(unarchiveKindSession, LocalRemoteID, sessionKey(platform, sessionID))
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
	return d.recordUnarchive(unarchiveKindSession, LocalRemoteID, sessionKey(platform, sessionID))
}

const (
	unarchiveKindSession = "session"
	unarchiveKindProject = "project"

	// LocalRemoteID is the routing/display sentinel for the machine ocman
	// itself runs on, matching hostsvc's local Host.
	LocalRemoteID = "local"
)

// NormalizeRemoteID maps the "unset" spellings of a host owner to the local
// sentinel, so a project row an adapter never stamped keys the same way as
// one the local host stamped itself.
func NormalizeRemoteID(remoteID string) string {
	if remoteID == "" {
		return LocalRemoteID
	}
	return remoteID
}

// ProjectKey is a project's full identity. The root alone is not one: with
// multi-remote, /home/u/app can exist on the hub and on every attached
// machine, and they are different projects.
type ProjectKey struct {
	RemoteID string
	Root     string
}

// sessionKey packs a (platform, session) pair into one unarchive key.
// Platform ids never contain a NUL, so the pair round-trips unambiguously.
// A session's owning remote is already encoded in its compound platform id
// (`r-<remote>:opencode`), so session rows always carry remote_id 'local'.
func sessionKey(platform, sessionID string) string {
	return platform + "\x00" + sessionID
}

// recordUnarchive stamps when the user last brought an entity back.
func (d *DB) recordUnarchive(kind, remoteID, key string) error {
	_, err := d.db.Exec(`
		INSERT INTO unarchived_entity (kind, remote_id, entity_key, unarchived_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(kind, remote_id, entity_key) DO UPDATE SET
			unarchived_at = excluded.unarchived_at
	`, kind, NormalizeRemoteID(remoteID), key, time.Now().UnixMilli())
	if err != nil {
		return fmt.Errorf("recording unarchive: %w", err)
	}
	return nil
}

// clearUnarchive drops the intent marker. Called when the entity is
// archived again: the newer archive supersedes the older unarchive.
func (d *DB) clearUnarchive(kind, remoteID, key string) error {
	_, err := d.db.Exec(
		`DELETE FROM unarchived_entity WHERE kind = ? AND remote_id = ? AND entity_key = ?`,
		kind, NormalizeRemoteID(remoteID), key,
	)
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
		platform, sessionID, found := strings.Cut(key.Root, "\x00")
		if !found {
			continue
		}
		out[Key{Platform: platform, SessionID: sessionID}] = true
	}
	return out, nil
}

// ProjectsUnarchivedSince returns the projects the user unarchived at or
// after cutoff, keyed by (remoteID, root) so a deliberate unarchive on one
// machine does not shield the same path on another.
func (d *DB) ProjectsUnarchivedSince(cutoff int64) (map[ProjectKey]bool, error) {
	return d.unarchivedSince(unarchiveKindProject, cutoff)
}

func (d *DB) unarchivedSince(kind string, cutoff int64) (map[ProjectKey]bool, error) {
	rows, err := d.db.Query(
		`SELECT remote_id, entity_key FROM unarchived_entity WHERE kind = ? AND unarchived_at >= ?`,
		kind, cutoff,
	)
	if err != nil {
		return nil, fmt.Errorf("listing unarchived entities: %w", err)
	}
	defer rows.Close()
	out := map[ProjectKey]bool{}
	for rows.Next() {
		var key ProjectKey
		if err := rows.Scan(&key.RemoteID, &key.Root); err != nil {
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

// ArchiveProject records a project as archived at the current time. The key
// is (remoteID, folded project root): the same path on another machine is a
// different project and keeps its own state. Re-archiving refreshes
// archived_at so future session activity can auto-unarchive it.
func (d *DB) ArchiveProject(remoteID, projectRoot string) error {
	_, err := d.db.Exec(`
		INSERT INTO archived_project (remote_id, project_root, archived_at)
		VALUES (?, ?, ?)
		ON CONFLICT(remote_id, project_root) DO UPDATE SET
			archived_at = excluded.archived_at
	`, NormalizeRemoteID(remoteID), projectRoot, time.Now().UnixMilli())
	if err != nil {
		return fmt.Errorf("archiving project: %w", err)
	}
	return d.clearUnarchive(unarchiveKindProject, remoteID, projectRoot)
}

// UnarchiveProject removes one host's archived marker for a project and
// records the user's intent so auto-archive does not immediately re-hide it.
func (d *DB) UnarchiveProject(remoteID, projectRoot string) error {
	_, err := d.db.Exec(
		`DELETE FROM archived_project WHERE remote_id = ? AND project_root = ?`,
		NormalizeRemoteID(remoteID), projectRoot,
	)
	if err != nil {
		return fmt.Errorf("unarchiving project: %w", err)
	}
	return d.recordUnarchive(unarchiveKindProject, remoteID, projectRoot)
}

// ArchivedProjects returns every archived project's archived_at time, keyed
// by (remoteID, folded project root).
func (d *DB) ArchivedProjects() (map[ProjectKey]int64, error) {
	rows, err := d.db.Query(`SELECT remote_id, project_root, archived_at FROM archived_project`)
	if err != nil {
		return nil, fmt.Errorf("listing archived projects: %w", err)
	}
	defer rows.Close()

	archived := make(map[ProjectKey]int64)
	for rows.Next() {
		var key ProjectKey
		var archivedAt int64
		if err := rows.Scan(&key.RemoteID, &key.Root, &archivedAt); err != nil {
			return nil, fmt.Errorf("scanning archived project: %w", err)
		}
		archived[key] = archivedAt
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading archived projects: %w", err)
	}

	return archived, nil
}
