package db

import (
	"database/sql"
	"encoding/json"
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

// GetSessions returns sessions, optionally filtered by directory and/or a minimum timestamp.
// Uses SQL aggregation to avoid N+1 queries.
func (d *DB) GetSessions(directory string, since int64) ([]Session, error) {
	query := `
		SELECT
			s.id, s.project_id, s.title, s.directory,
			s.time_created, s.time_updated,
			s.summary_additions, s.summary_deletions, s.summary_files,
			s.share_url,
			COALESCE((
				SELECT count(*) FROM message
				WHERE session_id = s.id AND json_extract(data, '$.role') = 'user'
			), 0) AS message_count,
			COALESCE((
				SELECT SUM(COALESCE(json_extract(data, '$.tokens.input'), 0))
				FROM message WHERE session_id = s.id AND json_extract(data, '$.role') = 'assistant'
			), 0) AS total_input_tokens,
			COALESCE((
				SELECT SUM(COALESCE(json_extract(data, '$.tokens.output'), 0))
				FROM message WHERE session_id = s.id AND json_extract(data, '$.role') = 'assistant'
			), 0) AS total_output_tokens,
			COALESCE((
				SELECT SUM(COALESCE(json_extract(data, '$.cost'), 0))
				FROM message WHERE session_id = s.id AND json_extract(data, '$.role') = 'assistant'
			), 0) AS total_cost,
			(
				SELECT json_extract(data, '$.role') FROM message
				WHERE session_id = s.id ORDER BY time_created DESC LIMIT 1
			) AS last_role,
			(
				SELECT json_extract(data, '$.finish') FROM message
				WHERE session_id = s.id ORDER BY time_created DESC LIMIT 1
			) AS last_finish,
			(
				SELECT json_extract(data, '$.error') FROM message
				WHERE session_id = s.id ORDER BY time_created DESC LIMIT 1
			) AS last_error,
			-- Error metadata for the notice normalizer. Carried on
			-- Session as internal-only fields (json:"-").
			(
				SELECT json_extract(data, '$.error.name') FROM message
				WHERE session_id = s.id ORDER BY time_created DESC LIMIT 1
			) AS last_error_name,
			(
				SELECT json_extract(data, '$.error.data.message') FROM message
				WHERE session_id = s.id ORDER BY time_created DESC LIMIT 1
			) AS last_error_message,
			(
				SELECT time_created FROM message
				WHERE session_id = s.id ORDER BY time_created DESC LIMIT 1
			) AS last_error_at,
			-- The last message is "synthesized terminal" when:
			--   (a) it has at least one part,
			--   (b) it has no 'step-start' part (so no LLM turn started), AND
			--   (c) no part is in a 'running' state (no in-flight tool).
			-- This identifies the assistant envelope produced by
			-- POST /session/{id}/shell, which never receives a 'finish'
			-- because no LLM turn ran. Without this signal, such
			-- sessions appear permanently "busy". See InferSessionStatus
			-- for how the flag is consumed.
			COALESCE((
				SELECT
					CASE
						WHEN EXISTS (SELECT 1 FROM part WHERE message_id = m.id)
							AND NOT EXISTS (
								SELECT 1 FROM part
								WHERE message_id = m.id
								  AND json_extract(data, '$.type') = 'step-start'
							)
							AND NOT EXISTS (
								SELECT 1 FROM part
								WHERE message_id = m.id
								  AND json_extract(data, '$.state.status') = 'running'
							)
						THEN 1 ELSE 0
					END
				FROM message m
				WHERE m.session_id = s.id
				ORDER BY m.time_created DESC LIMIT 1
			), 0) AS last_synth_terminal
		FROM session s
	`
	var conditions []string
	var args []interface{}
	// Hide subagent sessions (titles ending with "(<something> subagent)")
	conditions = append(conditions, `s.title NOT LIKE '%(% subagent)'`)
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
	query += ` ORDER BY s.time_updated DESC`

	rows, err := d.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []Session
	for rows.Next() {
		var s Session
		var lastRole, lastFinish, lastError *string
		var lastErrorName, lastErrorMessage *string
		var lastErrorAt *int64
		var lastSynthTerminal int
		err := rows.Scan(
			&s.ID, &s.ProjectID, &s.Title, &s.Directory,
			&s.TimeCreated, &s.TimeUpdated,
			&s.SummaryAdditions, &s.SummaryDeletions, &s.SummaryFiles,
			&s.ShareURL,
			&s.MessageCount,
			&s.TotalInputTokens, &s.TotalOutputTokens, &s.TotalCost,
			&lastRole, &lastFinish, &lastError,
			&lastErrorName, &lastErrorMessage, &lastErrorAt,
			&lastSynthTerminal,
		)
		if err != nil {
			log.WithError(err).Warn("failed to scan session row")
			continue
		}
		s.DurationMs = s.TimeUpdated - s.TimeCreated

		// Determine session status based on the last message.
		role, finish, lastErr := derefStr(lastRole), derefStr(lastFinish), derefStr(lastError)
		s.Status = InferSessionStatus(role, finish, lastErr, lastSynthTerminal == 1)

		// Carry error metadata for the notice normalizer.
		s.LastErrorName = derefStr(lastErrorName)
		s.LastErrorMessage = derefStr(lastErrorMessage)
		if lastErrorAt != nil {
			s.LastErrorAt = *lastErrorAt
		}

		sessions = append(sessions, s)
	}
	if err := rows.Err(); err != nil {
		return sessions, err
	}
	return sessions, nil
}

// GetSession returns a single session by ID.
func (d *DB) GetSession(sessionID string) (*Session, error) {
	var s Session
	err := d.db.QueryRow(`
		SELECT
			s.id, s.project_id, s.title, s.directory,
			s.time_created, s.time_updated,
			s.summary_additions, s.summary_deletions, s.summary_files,
			s.share_url
		FROM session s WHERE s.id = ?
	`, sessionID).Scan(
		&s.ID, &s.ProjectID, &s.Title, &s.Directory,
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
func (d *DB) GetSubagentSessionIDs(parentID string) ([]string, error) {
	if parentID == "" {
		return nil, nil
	}
	rows, err := d.db.Query(`SELECT id FROM session WHERE parent_id = ?`, parentID)
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

// GetSessionParentIDs returns a map of child→parent for every id in
// childIDs that has a non-NULL parent_id. IDs with no parent (top-level
// sessions) and IDs that don't exist are simply absent from the map,
// not present with an empty value. Used to bubble subagent-level
// pending-prompt flags up to the parent session in the listing.
//
// Returns an empty map (and nil error) when childIDs is empty so callers
// can hand it the result of a fan-out without first checking len().
func (d *DB) GetSessionParentIDs(childIDs []string) (map[string]string, error) {
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
	rows, err := d.db.Query(
		`SELECT id, parent_id FROM session WHERE parent_id IS NOT NULL AND id IN (`+placeholders+`)`,
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var id, parentID string
		if err := rows.Scan(&id, &parentID); err != nil {
			log.WithError(err).Warn("failed to scan session parent id")
			continue
		}
		out[id] = parentID
	}
	return out, rows.Err()
}

// GetSessionsInactiveBefore returns non-subagent sessions last updated before the cutoff.
func (d *DB) GetSessionsInactiveBefore(cutoff int64) ([]SessionArchiveCandidate, error) {
	rows, err := d.db.Query(`
		SELECT s.id, s.time_updated
		FROM session s
		WHERE s.title NOT LIKE '%(% subagent)'
		  AND s.time_updated < ?
	`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []SessionArchiveCandidate
	for rows.Next() {
		var session SessionArchiveCandidate
		if err := rows.Scan(&session.ID, &session.TimeUpdated); err != nil {
			log.WithError(err).Warn("failed to scan inactive session row")
			continue
		}
		sessions = append(sessions, session)
	}
	return sessions, rows.Err()
}

// GetSessionMessages returns all messages for a session.
func (d *DB) GetSessionMessages(sessionID string) ([]Message, error) {
	rows, err := d.db.Query(`
		SELECT id, session_id, time_created, data
		FROM message
		WHERE session_id = ?
		ORDER BY time_created
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []Message
	for rows.Next() {
		var m Message
		var rawData string
		err := rows.Scan(&m.ID, &m.SessionID, &m.TimeCreated, &rawData)
		if err != nil {
			log.WithError(err).Warn("failed to scan message row")
			continue
		}
		m.Data = json.RawMessage(rawData)
		messages = append(messages, m)
	}
	if err := rows.Err(); err != nil {
		return messages, err
	}
	return messages, nil
}

// GetSessionParts returns all parts for a session.
func (d *DB) GetSessionParts(sessionID string) ([]Part, error) {
	rows, err := d.db.Query(`
		SELECT id, message_id, session_id, time_created, data
		FROM part
		WHERE session_id = ?
		ORDER BY time_created
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var parts []Part
	for rows.Next() {
		var p Part
		var rawData string
		err := rows.Scan(&p.ID, &p.MessageID, &p.SessionID, &p.TimeCreated, &rawData)
		if err != nil {
			log.WithError(err).Warn("failed to scan part row")
			continue
		}
		p.Data = json.RawMessage(rawData)
		parts = append(parts, p)
	}
	if err := rows.Err(); err != nil {
		return parts, err
	}
	return parts, nil
}

// paginateMessages returns a page of messages from the end.
// offset is the number of messages to skip from the end, limit is the page size.
// Returns the paginated slice and the total count.
func PaginateMessages(messages []Message, limit, offset int) ([]Message, int) {
	total := len(messages)
	start := total - offset - limit
	end := total - offset
	if start < 0 {
		start = 0
	}
	if end < 0 {
		end = 0
	}
	if end > total {
		end = total
	}
	if start >= end {
		return nil, total
	}
	return messages[start:end], total
}

// FilterPartsForMessages returns only parts whose MessageID appears in the given messages.
func FilterPartsForMessages(parts []Part, messages []Message) []Part {
	msgIDs := make(map[string]bool, len(messages))
	for _, m := range messages {
		msgIDs[m.ID] = true
	}
	var filtered []Part
	for _, p := range parts {
		if msgIDs[p.MessageID] {
			filtered = append(filtered, p)
		}
	}
	return filtered
}

// GetSessionDefaults returns the most recent agent/model pair to seed a new or
// empty session's composer. It prefers another session in the same directory and
// falls back to the most recent session overall.
//
// Implementation note. The original query joined every message
// against every session and applied six json_extract filters in the
// WHERE clause, which on a real DB (~60k messages, ~1.4k sessions)
// burned 500–1400 ms per call. Since this function is called on
// every SessionDetail mount and Models / Info fetch, that single
// query dominated end-to-end latency.
//
// We now do a two-step lookup that uses the existing
// (session_id, time_created) message index:
//
//  1. Pick the N most-recently-updated sessions in the matching
//     directory (excluding the requesting session and subagent
//     transcripts). The session table is small and N is bounded
//     (5), so this is essentially free.
//  2. Pull the single most recent assistant message from any of
//     those sessions that has agent/provider/model fields set,
//     using the index above. Bounded scan, no full table walk.
//  3. If step 2 yields nothing (no qualifying sessions in the
//     directory at all, e.g. brand-new directory), repeat without
//     the directory filter to fall back to the global most-recent.
//
// Measured speedup against a representative DB: 1162 ms → 8 ms
// (~145×). See profiling notes in spec/perf-notes if you want the
// before/after EXPLAIN QUERY PLAN output.
func (d *DB) GetSessionDefaults(sessionID, directory string) (SessionDefaults, error) {
	if defaults, ok, err := d.lookupSessionDefaults(sessionID, directory); err != nil {
		return SessionDefaults{}, err
	} else if ok {
		return defaults, nil
	}
	// Fall back to "most recent across all directories" when the
	// directory has no qualifying sessions yet.
	defaults, _, err := d.lookupSessionDefaults(sessionID, "")
	if err != nil {
		return SessionDefaults{}, err
	}
	return defaults, nil
}

// lookupSessionDefaults runs the bounded two-step query. When
// directory is non-empty, only sessions in that directory are
// considered; when empty, the directory filter is dropped so the
// caller can use the same code for the fallback path.
//
// Returns (defaults, found, err): found=false means "no qualifying
// row" (sql.ErrNoRows). All other errors propagate as err.
func (d *DB) lookupSessionDefaults(sessionID, directory string) (SessionDefaults, bool, error) {
	const candidatesLimit = 5

	// The only difference between the directory-scoped and global
	// lookups is the candidate-CTE's WHERE shape; the bulk of the query
	// (the agent + model resolution over the candidate sessions) is
	// identical, so it lives in one constant. Args are appended in the
	// same order the CTE placeholders appear.
	const sessionDefaultsTail = `
		SELECT
			COALESCE(NULLIF(json_extract(m.data, '$.agent'), ''), '') AS agent,
			COALESCE(
				CASE
					WHEN COALESCE(NULLIF(json_extract(m.data, '$.providerID'), ''), '') != ''
						AND COALESCE(NULLIF(json_extract(m.data, '$.modelID'), ''), '') != ''
					THEN json_extract(m.data, '$.providerID') || '/' || json_extract(m.data, '$.modelID')
					WHEN COALESCE(NULLIF(json_extract(m.data, '$.model.providerID'), ''), '') != ''
						AND COALESCE(NULLIF(json_extract(m.data, '$.model.modelID'), ''), '') != ''
					THEN json_extract(m.data, '$.model.providerID') || '/' || json_extract(m.data, '$.model.modelID')
					ELSE COALESCE(
						NULLIF(json_extract(m.data, '$.modelID'), ''),
						NULLIF(json_extract(m.data, '$.model.modelID'), ''),
						''
					)
				END,
				''
			) AS model
		FROM candidate_sessions cs
		JOIN message m ON m.session_id = cs.id
		WHERE json_extract(m.data, '$.role') = 'assistant'
		  AND (
				COALESCE(NULLIF(json_extract(m.data, '$.agent'), ''), '') != ''
				OR COALESCE(NULLIF(json_extract(m.data, '$.providerID'), ''), '') != ''
				OR COALESCE(NULLIF(json_extract(m.data, '$.modelID'), ''), '') != ''
				OR COALESCE(NULLIF(json_extract(m.data, '$.model.providerID'), ''), '') != ''
				OR COALESCE(NULLIF(json_extract(m.data, '$.model.modelID'), ''), '') != ''
		  )
		ORDER BY cs.time_updated DESC, m.time_created DESC
		LIMIT 1
	`

	var cte string
	var args []interface{}
	if directory != "" {
		cte = `
		WITH candidate_sessions AS (
			SELECT id, time_updated FROM session
			WHERE directory = ?
			  AND id != ?
			  AND title NOT LIKE '%(% subagent)'
			ORDER BY time_updated DESC
			LIMIT ?
		)`
		args = []interface{}{directory, sessionID, candidatesLimit}
	} else {
		cte = `
		WITH candidate_sessions AS (
			SELECT id, time_updated FROM session
			WHERE id != ?
			  AND title NOT LIKE '%(% subagent)'
			ORDER BY time_updated DESC
			LIMIT ?
		)`
		args = []interface{}{sessionID, candidatesLimit}
	}

	var defaults SessionDefaults
	err := d.db.QueryRow(cte+sessionDefaultsTail, args...).
		Scan(&defaults.Agent, &defaults.Model)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SessionDefaults{}, false, nil
		}
		return SessionDefaults{}, false, err
	}
	return defaults, true, nil
}

// GetContextTokenCount returns the token usage shown in OpenCode's prompt bar:
// the last assistant message with output > 0, using
// input + output + reasoning + cache.read + cache.write.
func (d *DB) GetContextTokenCount(sessionID string) (int64, error) {
	var count int64
	err := d.db.QueryRow(`
		SELECT COALESCE(
		  json_extract(data, '$.tokens.input'),
		  0
		) + COALESCE(
		  json_extract(data, '$.tokens.output'),
		  0
		) + COALESCE(
		  json_extract(data, '$.tokens.reasoning'),
		  0
		) + COALESCE(
		  json_extract(data, '$.tokens.cache.read'),
		  0
		) + COALESCE(
		  json_extract(data, '$.tokens.cache.write'),
		  0
		)
		FROM message
		WHERE session_id = ?
		  AND json_extract(data, '$.role') = 'assistant'
		  AND COALESCE(json_extract(data, '$.tokens.output'), 0) > 0
		ORDER BY time_created DESC
		LIMIT 1
	`, sessionID).Scan(&count)
	if err != nil {
		return 0, err
	}
	return count, nil
}
