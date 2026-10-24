package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	log "github.com/sirupsen/logrus"
)

// derefStr returns the string value of a nullable string pointer, or "" if nil.
func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// GetSessions returns sessions, optionally filtered by directory and/or a
// minimum timestamp. Message totals and latest-message fields are computed
// in shared CTEs rather than correlated once per session row.
func (d *DB) GetSessions(ctx context.Context, directory string, since int64) ([]Session, error) {
	query := sessionListSelection
	var conditions []string
	var args []interface{}
	// Subagent sessions (non-NULL parent_id, conventionally titled
	// "... (<something> subagent)") are no longer hidden in SQL — we
	// surface them nested under their parent in the UI. Completed
	// subagents are filtered out below in Go, after status inference,
	// to keep the list from filling up with finished Task-tool runs.
	if directory != "" {
		conditions = append(conditions, `s.directory = ?`)
		args = append(args, directory)
	}
	if since > 0 {
		conditions = append(conditions, `s.time_updated >= ?`)
		args = append(args, since)
	}
	if len(conditions) > 0 {
		query += ` WHERE ` + strings.Join(conditions, ` AND `)
	}
	query += d.sessionListProjection() + ` ORDER BY s.time_updated DESC`

	rows, err := d.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []Session
	for rows.Next() {
		s, keep, err := scanSessionRow(rows.Scan)
		if err != nil {
			log.WithError(err).Warn("failed to scan session row")
			continue
		}
		if !keep {
			continue
		}
		sessions = append(sessions, s)
	}
	if err := rows.Err(); err != nil {
		return sessions, err
	}
	return sessions, nil
}

// GetSession returns session metadata by ID without reading message history.
func (d *DB) GetSession(ctx context.Context, sessionID string) (*Session, error) {
	var s Session
	err := d.db.QueryRowContext(ctx, `
		SELECT
			s.id, s.project_id, COALESCE(s.parent_id, ''), s.title, s.directory,
			s.time_created, s.time_updated,
			s.summary_additions, s.summary_deletions, s.summary_files,
			s.share_url
		FROM session s WHERE s.id = ?
	`, sessionID).Scan(
		&s.ID, &s.ProjectID, &s.ParentID, &s.Title, &s.Directory,
		&s.TimeCreated, &s.TimeUpdated,
		&s.SummaryAdditions, &s.SummaryDeletions, &s.SummaryFiles,
		&s.ShareURL,
	)
	if err != nil {
		return nil, err
	}
	s.DurationMs = s.TimeUpdated - s.TimeCreated
	return &s, nil
}

// GetSubagentSessionIDs returns the IDs of every session whose parent_id
// equals parentID. Used to bubble subagent-level prompts (permission /
// question) up to the parent session in the UI: when an OpenCode subagent
// asks for permission, the prompt is emitted with the subagent's own
// session ID, but the user only sees and acts on the parent session.
//
// Returns an empty slice (and nil error) when parentID is empty so callers
// can blindly invoke this for any session without first checking whether
// it might have children.
func (d *DB) GetSubagentSessionIDs(ctx context.Context, parentID string) ([]string, error) {
	if parentID == "" {
		return nil, nil
	}
	rows, err := d.db.QueryContext(ctx, `SELECT id FROM session WHERE parent_id = ?`, parentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			log.WithError(err).Warn("failed to scan subagent session id")
			continue
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ProjectDirectory maps an OpenCode project ID to its root directory.
// Sibling worktrees share one project ID, so session rows alone are
// ambiguous; OpenCode's project table holds exactly one root per ID
// (`worktree`, the main checkout), which makes the answer deterministic.
// The session rows gate it: only a project with at least one session is
// known. Unknown IDs and OpenCode's non-git "global" project (root "/")
// return "", omitting the evidence. A database without a project table is an
// error, so a snapshot never silently drops project metadata.
func (d *DB) ProjectDirectory(ctx context.Context, projectID string) (string, error) {
	if projectID == "" {
		return "", nil
	}
	var directory string
	err := d.db.QueryRowContext(ctx, `SELECT p.worktree FROM project p
		WHERE p.id = ? AND p.worktree NOT IN ('', '/')
		AND EXISTS (SELECT 1 FROM session s WHERE s.project_id = p.id)`, projectID).Scan(&directory)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return directory, err
}

// GetSessionParentIDs returns a map of child→top-level-ancestor for
// every id in childIDs that has a non-NULL parent_id. The value is the
// *outermost* ancestor (the top-level session with no parent_id), not
// just the immediate parent, so a prompt on a deeply nested subagent —
// e.g. a Task subagent launched by another subagent, outside ocman's
// control — still bubbles up to the visible row in the listing. IDs
// with no parent (top-level sessions) and IDs that don't exist are
// simply absent from the map, not present with an empty value.
//
// Returns an empty map (and nil error) when childIDs is empty so callers
// can hand it the result of a fan-out without first checking len().
func (d *DB) GetSessionParentIDs(ctx context.Context, childIDs []string) (map[string]string, error) {
	out := make(map[string]string, len(childIDs))
	if len(childIDs) == 0 {
		return out, nil
	}
	// Build "?, ?, ?" placeholders for an IN clause.
	placeholders := strings.Repeat("?,", len(childIDs))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]interface{}, len(childIDs))
	for i, id := range childIDs {
		args[i] = id
	}
	// Recursive CTE: seed with each requested child, then walk parent_id
	// up until the ancestor has none. The final SELECT keeps only the
	// row where the ancestor is top-level (parent_id IS NULL), giving one
	// (start, top-level ancestor) pair per requested id that has a parent.
	//
	// ponytail: bounded by real session nesting depth (a handful);
	// SQLite's default recursion limit (1000) is the safety ceiling.
	rows, err := d.db.QueryContext(ctx,
		`WITH RECURSIVE anc(start, id, parent_id) AS (
			SELECT id, id, parent_id FROM session
			WHERE parent_id IS NOT NULL AND id IN (`+placeholders+`)
			UNION ALL
			SELECT anc.start, s.id, s.parent_id
			FROM session s JOIN anc ON s.id = anc.parent_id
		)
		SELECT start, id FROM anc WHERE parent_id IS NULL`,
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var startID, ancestorID string
		if err := rows.Scan(&startID, &ancestorID); err != nil {
			log.WithError(err).Warn("failed to scan session parent id")
			continue
		}
		out[startID] = ancestorID
	}
	return out, rows.Err()
}

// ErrAmbiguousRunningTool prevents guessing when multiple sessions match.
var ErrAmbiguousRunningTool = errors.New("multiple sessions are invoking the tool")

// FindRunningToolSessionID returns the only session currently invoking a
// tool. OpenCode writes the running tool part before making the MCP request.
func (d *DB) FindRunningToolSessionID(ctx context.Context, tool, directory string) (string, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT DISTINCT p.session_id
		FROM part p
		JOIN session s ON s.id = p.session_id
		WHERE json_extract(p.data, '$.type') = 'tool'
		  AND json_extract(p.data, '$.tool') IN (?, 'ocman_' || ?)
		  AND json_extract(p.data, '$.state.status') = 'running'
		  AND (? = '' OR s.directory = ?)
		LIMIT 2
	`, tool, tool, directory, directory)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	var sessionID string
	if !rows.Next() {
		return "", rows.Err()
	}
	if err := rows.Scan(&sessionID); err != nil {
		return "", err
	}
	if rows.Next() {
		return "", ErrAmbiguousRunningTool
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return sessionID, nil
}

// GetSessionDefaults returns the most recent agent/model pair to seed a new or
// empty session's composer. It prefers another session in the same directory and
// falls back to the most recent session overall. Only history readers carry the
// compact facts it reads.
func (d *DB) GetSessionDefaults(ctx context.Context, sessionID, directory string) (SessionDefaults, error) {
	if defaults, ok, err := d.historySessionDefaults(ctx, sessionID, directory); err != nil || ok {
		return defaults, err
	}
	defaults, _, err := d.historySessionDefaults(ctx, sessionID, "")
	return defaults, err
}
