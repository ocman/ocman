package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// Callers select sessions before computing message aggregates.
const sessionListSelection = `
		WITH selected_sessions AS (
			SELECT * FROM session s`

// GetSessions and GetSessionSummary share this projection. Message blobs may
// contain huge summary.diffs: find the latest message through its covering
// index, count messages index-only, and prefer denormalized session totals.
func (d *DB) sessionListProjection() string {
	agg := legacyTotalsAggregate
	if d.sessionTotals {
		agg = sessionTotalsAggregate
	}
	return `
		),
		message_aggregate AS (` + agg + `
		)
		SELECT
			s.id, s.project_id, s.parent_id, s.title, s.directory,
			s.time_created, s.time_updated, ` + lastTurnCompletedAtSQL + `, ` + lastUserPromptAtSQL + `,
			s.summary_additions, s.summary_deletions, s.summary_files,
			s.share_url,
			(SELECT count(*) FROM message m WHERE m.session_id = s.id) AS message_count,
			COALESCE(ma.total_input_tokens, 0) AS total_input_tokens,
			COALESCE(ma.total_output_tokens, 0) AS total_output_tokens,
			COALESCE(ma.total_cost, 0) AS total_cost,
			json_extract(lm.data, '$.role') AS last_role,
			json_extract(lm.data, '$.finish') AS last_finish,
			json_extract(lm.data, '$.error') AS last_error,
			json_extract(lm.data, '$.error.name') AS last_error_name,
			COALESCE(json_extract(lm.data, '$.error.data.message'), json_extract(lm.data, '$.error.message')) AS last_error_message,
			lm.time_created AS last_error_at,
			-- Synthesized shell envelopes have parts, no LLM step and no running tool.
			CASE
				WHEN EXISTS (SELECT 1 FROM part WHERE message_id = lm.id)
					AND NOT EXISTS (
						SELECT 1 FROM part
						WHERE message_id = lm.id
						  AND json_extract(data, '$.type') = 'step-start'
					)
					AND NOT EXISTS (
						SELECT 1 FROM part
						WHERE message_id = lm.id
						  AND json_extract(data, '$.state.status') = 'running'
					)
				THEN 1 ELSE 0
			END AS last_synth_terminal
		FROM selected_sessions s
		LEFT JOIN message_aggregate ma ON ma.session_id = s.id
		LEFT JOIN message lm ON lm.id = (
			SELECT id FROM message
			WHERE session_id = s.id
			ORDER BY time_created DESC, id DESC
			LIMIT 1
		)
	`
}

const sessionTotalsAggregate = `
			SELECT
				s.id AS session_id,
				s.tokens_input AS total_input_tokens,
				s.tokens_output AS total_output_tokens,
				s.cost AS total_cost
			FROM selected_sessions s`

const legacyTotalsAggregate = `
			SELECT
				m.session_id,
				SUM(CASE WHEN json_extract(m.data, '$.role') = 'assistant'
					THEN COALESCE(json_extract(m.data, '$.tokens.input'), 0) ELSE 0 END) AS total_input_tokens,
				SUM(CASE WHEN json_extract(m.data, '$.role') = 'assistant'
					THEN COALESCE(json_extract(m.data, '$.tokens.output'), 0) ELSE 0 END) AS total_output_tokens,
				SUM(CASE WHEN json_extract(m.data, '$.role') = 'assistant'
					THEN COALESCE(json_extract(m.data, '$.cost'), 0) ELSE 0 END) AS total_cost
			FROM message m
			JOIN selected_sessions s ON s.id = m.session_id
			GROUP BY m.session_id`

var ErrSessionNotFound = errors.New("session not found in the session list")

// Scan and derive every session row identically. The owning adapter settles
// the provisional status against the live lifecycle before publication.
func scanSessionRow(scan func(dest ...any) error) (s Session, keep bool, err error) {
	var parentID *string
	var lastRole, lastFinish, lastError *string
	var lastErrorName, lastErrorMessage *string
	var lastErrorAt *int64
	var lastSynthTerminal int
	err = scan(
		&s.ID, &s.ProjectID, &parentID, &s.Title, &s.Directory,
		&s.TimeCreated, &s.TimeUpdated, &s.LastTurnCompletedAt, &s.LastUserPromptAt,
		&s.SummaryAdditions, &s.SummaryDeletions, &s.SummaryFiles,
		&s.ShareURL,
		&s.MessageCount,
		&s.TotalInputTokens, &s.TotalOutputTokens, &s.TotalCost,
		&lastRole, &lastFinish, &lastError,
		&lastErrorName, &lastErrorMessage, &lastErrorAt,
		&lastSynthTerminal,
	)
	if err != nil {
		return Session{}, false, err
	}
	s.ParentID = derefStr(parentID)
	s.DurationMs = s.TimeUpdated - s.TimeCreated
	role, finish, lastErr := derefStr(lastRole), derefStr(lastFinish), derefStr(lastError)
	s.Status = InferSessionStatus(role, finish, lastErr, lastSynthTerminal == 1)
	s.LastErrorName = derefStr(lastErrorName)
	s.LastErrorMessage = derefStr(lastErrorMessage)
	if lastErrorAt != nil {
		s.LastErrorAt = *lastErrorAt
	}
	// Parentless helper sessions must stay hidden even when fetched directly.
	if s.ParentID == "" && strings.HasSuffix(s.Title, " subagent)") {
		return s, false, nil
	}
	return s, true, nil
}

// GetSessionSummary incrementally refreshes exactly the list's projection.
func (d *DB) GetSessionSummary(ctx context.Context, sessionID string) (Session, error) {
	row := d.db.QueryRowContext(ctx, sessionListSelection+` WHERE s.id = ?`+d.sessionListProjection(), sessionID)
	s, keep, err := scanSessionRow(row.Scan)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Session{}, ErrSessionNotFound
		}
		return Session{}, err
	}
	if !keep {
		return Session{}, ErrSessionNotFound
	}
	return s, nil
}
