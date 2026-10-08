package db

import "context"

// GetSessionIdentities enumerates fresh owner-local candidates without the
// display list's cache or inactive-child filters. Lifecycle reads decide which
// turns a shared-server replacement would interrupt.
func (d *DB) GetSessionIdentities(ctx context.Context) ([]Session, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT id, directory FROM session ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sessions []Session
	for rows.Next() {
		var session Session
		if err := rows.Scan(&session.ID, &session.Directory); err != nil {
			return nil, err
		}
		sessions = append(sessions, session)
	}
	return sessions, rows.Err()
}

// GetSessionStatus infers status from only the latest message.
func (d *DB) GetSessionStatus(ctx context.Context, sessionID string) (SessionStatus, error) {
	l, err := d.GetSessionLifecycle(ctx, sessionID)
	return l.Status, err
}

// SessionLifecycle is a session's directory, inferred (unsettled) status and
// latest message identity. LatestMessageID is empty for a session with no
// messages.
type SessionLifecycle struct {
	Directory            string
	Status               SessionStatus
	LatestMessageID      string
	LatestMessageCreated int64
	LatestMessageRole    string
	LatestMessageFinish  string
	LatestErrorName      string
	LatestCompleted      int64
}

// GetSessionLifecycle reads one session row and its latest message. It
// selects that message's ID before extracting JSON. An unclosed assistant
// envelope also checks its latest parts' terminal metadata, without loading
// parts or historical message blobs: the cost is independent of transcript size.
func (d *DB) GetSessionLifecycle(ctx context.Context, sessionID string) (SessionLifecycle, error) {
	var l SessionLifecycle
	var finish, lastError string
	err := d.db.QueryRowContext(ctx, `
		SELECT s.directory,
		       COALESCE(m.id, ''), COALESCE(m.time_created, 0),
		       COALESCE(json_extract(m.data, '$.role'), ''),
		       COALESCE(json_extract(m.data, '$.finish'), ''),
		       COALESCE(json_extract(m.data, '$.error'), ''),
		       COALESCE(json_extract(m.data, '$.error.name'), ''),
		       COALESCE(json_extract(m.data, '$.time.completed'), 0)
		FROM session s
		LEFT JOIN message m ON m.id = (
			SELECT id FROM message WHERE session_id = s.id
			ORDER BY time_created DESC, id DESC LIMIT 1
		)
		WHERE s.id = ?
	`, sessionID).Scan(&l.Directory, &l.LatestMessageID, &l.LatestMessageCreated,
		&l.LatestMessageRole, &finish, &lastError, &l.LatestErrorName, &l.LatestCompleted)
	if err != nil {
		return SessionLifecycle{}, err
	}
	synthesizedTerminal := false
	if l.LatestMessageRole == "assistant" && finish == "" && lastError == "" {
		// Same synthesized-terminal criteria as the session summary: completed
		// shell envelopes have parts, no LLM step, and no running tool.
		err := d.db.QueryRowContext(ctx, `SELECT
			EXISTS (SELECT 1 FROM part WHERE message_id = ?)
			AND NOT EXISTS (SELECT 1 FROM part WHERE message_id = ? AND json_extract(data, '$.type') = 'step-start')
			AND NOT EXISTS (SELECT 1 FROM part WHERE message_id = ? AND json_extract(data, '$.state.status') = 'running')
		`, l.LatestMessageID, l.LatestMessageID, l.LatestMessageID).Scan(&synthesizedTerminal)
		if err != nil {
			return SessionLifecycle{}, err
		}
	}
	l.Status = InferSessionStatus(l.LatestMessageRole, finish, lastError, synthesizedTerminal)
	l.LatestMessageFinish = finish
	return l, nil
}

// StatusCandidateDirectories returns the distinct directories that may hold a
// running turn: any session updated at or after sinceMs, plus any worktree
// session, however old, whose latest message finished with "tool-calls" or
// "unknown". OpenCode's prompt loop treats those finishes as non-terminal
// (the turn continues, e.g. blocked on a permission), so age proves nothing.
// It reads the tables directly, never the cached snapshot. The finish check
// is limited to worktree paths: root directories are read unscoped anyway,
// and the limit keeps the per-session JSON lookup off most rows.
func (d *DB) StatusCandidateDirectories(ctx context.Context, sinceMs int64) ([]string, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT DISTINCT s.directory FROM session s
		WHERE s.time_updated >= ?
		   OR (s.directory LIKE '%/.worktrees/%' AND (
			SELECT json_extract(m.data, '$.finish') FROM message m
			WHERE m.session_id = s.id
			ORDER BY m.time_created DESC, m.id DESC LIMIT 1
		   ) IN ('tool-calls', 'unknown'))`, sinceMs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var directory string
		if err := rows.Scan(&directory); err != nil {
			return nil, err
		}
		out = append(out, directory)
	}
	return out, rows.Err()
}
