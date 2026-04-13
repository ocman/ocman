package db

import (
	"database/sql"
	"encoding/json"
	"strings"

	log "github.com/sirupsen/logrus"
)

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
			) AS last_finish
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
		var lastRole, lastFinish *string
		err := rows.Scan(
			&s.ID, &s.ProjectID, &s.Title, &s.Directory,
			&s.TimeCreated, &s.TimeUpdated,
			&s.SummaryAdditions, &s.SummaryDeletions, &s.SummaryFiles,
			&s.ShareURL,
			&s.MessageCount,
			&s.TotalInputTokens, &s.TotalOutputTokens, &s.TotalCost,
			&lastRole, &lastFinish,
		)
		if err != nil {
			log.WithError(err).Warn("failed to scan session row")
			continue
		}
		s.DurationMs = s.TimeUpdated - s.TimeCreated

		// Determine session status based on the last message.
		// "waiting" = last assistant message has a finish reason (turn complete, needs user input)
		// "busy"    = last message is assistant with no finish reason (still streaming)
		// "done"    = no messages or last message is from the user (session idle)
		if lastRole != nil && *lastRole == "assistant" {
			if lastFinish != nil && *lastFinish != "" {
				s.Status = "waiting"
			} else {
				s.Status = "busy"
			}
		} else {
			s.Status = "done"
		}

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

// sessionRowScannable is a helper type for scanning nullable int64 values.
type nullableInt64 = sql.NullInt64
