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
			) AS last_error
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
		err := rows.Scan(
			&s.ID, &s.ProjectID, &s.Title, &s.Directory,
			&s.TimeCreated, &s.TimeUpdated,
			&s.SummaryAdditions, &s.SummaryDeletions, &s.SummaryFiles,
			&s.ShareURL,
			&s.MessageCount,
			&s.TotalInputTokens, &s.TotalOutputTokens, &s.TotalCost,
			&lastRole, &lastFinish, &lastError,
		)
		if err != nil {
			log.WithError(err).Warn("failed to scan session row")
			continue
		}
		s.DurationMs = s.TimeUpdated - s.TimeCreated

		// Determine session status based on the last message.
		role, finish, lastErr := derefStr(lastRole), derefStr(lastFinish), derefStr(lastError)
		s.Status = InferSessionStatus(role, finish, lastErr)

		sessions = append(sessions, s)
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
func (d *DB) GetSessionDefaults(sessionID, directory string) (SessionDefaults, error) {
	var defaults SessionDefaults
	err := d.db.QueryRow(`
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
		FROM message m
		JOIN session s ON s.id = m.session_id
		WHERE m.session_id != ?
		  AND s.title NOT LIKE '%(% subagent)'
		  AND json_extract(m.data, '$.role') = 'assistant'
		  AND (
				COALESCE(NULLIF(json_extract(m.data, '$.agent'), ''), '') != ''
				OR COALESCE(NULLIF(json_extract(m.data, '$.providerID'), ''), '') != ''
				OR COALESCE(NULLIF(json_extract(m.data, '$.modelID'), ''), '') != ''
				OR COALESCE(NULLIF(json_extract(m.data, '$.model.providerID'), ''), '') != ''
				OR COALESCE(NULLIF(json_extract(m.data, '$.model.modelID'), ''), '') != ''
		  )
		ORDER BY CASE WHEN s.directory = ? THEN 0 ELSE 1 END, m.time_created DESC
		LIMIT 1
	`, sessionID, directory).Scan(&defaults.Agent, &defaults.Model)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SessionDefaults{}, nil
		}
		return SessionDefaults{}, err
	}
	return defaults, nil
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
