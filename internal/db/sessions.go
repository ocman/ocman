package db

import (
	"context"
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

// sessionListSelection starts THE session-list query. Callers add their
// predicate here so the aggregate and latest-message CTEs only process
// messages belonging to selected sessions.
const sessionListSelection = `
		WITH selected_sessions AS (
			SELECT * FROM session s`

// sessionListProjection completes the shared session-list projection.
// Both GetSessions and GetSessionSummary use these exact expressions, so
// an incremental refresh cannot drift from a full scan.
const sessionListProjection = `
		),
		message_aggregate AS (
			SELECT
				m.session_id,
				SUM(CASE WHEN json_extract(m.data, '$.role') = 'user' THEN 1 ELSE 0 END) AS message_count,
				SUM(CASE WHEN json_extract(m.data, '$.role') = 'assistant'
					THEN COALESCE(json_extract(m.data, '$.tokens.input'), 0) ELSE 0 END) AS total_input_tokens,
				SUM(CASE WHEN json_extract(m.data, '$.role') = 'assistant'
					THEN COALESCE(json_extract(m.data, '$.tokens.output'), 0) ELSE 0 END) AS total_output_tokens,
				SUM(CASE WHEN json_extract(m.data, '$.role') = 'assistant'
					THEN COALESCE(json_extract(m.data, '$.cost'), 0) ELSE 0 END) AS total_cost
			FROM message m
			JOIN selected_sessions s ON s.id = m.session_id
			GROUP BY m.session_id
		),
		ranked_messages AS (
			SELECT
				m.id, m.session_id, m.time_created, m.data,
				ROW_NUMBER() OVER (
					PARTITION BY m.session_id ORDER BY m.time_created DESC
				) AS message_rank
			FROM message m
			JOIN selected_sessions s ON s.id = m.session_id
		),
		latest_message AS (
			SELECT id, session_id, time_created, data
			FROM ranked_messages
			WHERE message_rank = 1
		)
		SELECT
			s.id, s.project_id, s.parent_id, s.title, s.directory,
			s.time_created, s.time_updated,
			s.summary_additions, s.summary_deletions, s.summary_files,
			s.share_url,
			COALESCE(ma.message_count, 0) AS message_count,
			COALESCE(ma.total_input_tokens, 0) AS total_input_tokens,
			COALESCE(ma.total_output_tokens, 0) AS total_output_tokens,
			COALESCE(ma.total_cost, 0) AS total_cost,
			json_extract(lm.data, '$.role') AS last_role,
			json_extract(lm.data, '$.finish') AS last_finish,
			json_extract(lm.data, '$.error') AS last_error,
			-- Error metadata for the notice normalizer. Carried on
			-- Session as internal-only fields (json:"-").
			json_extract(lm.data, '$.error.name') AS last_error_name,
			COALESCE(json_extract(lm.data, '$.error.data.message'), json_extract(lm.data, '$.error.message')) AS last_error_message,
			lm.time_created AS last_error_at,
			-- The last message is "synthesized terminal" when:
			--   (a) it has at least one part,
			--   (b) it has no 'step-start' part (so no LLM turn started), AND
			--   (c) no part is in a 'running' state (no in-flight tool).
			-- This identifies the assistant envelope produced by
			-- POST /session/{id}/shell, which never receives a 'finish'
			-- because no LLM turn ran. Without this signal, such
			-- sessions appear permanently "busy". See InferSessionStatus
			-- for how the flag is consumed.
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
		LEFT JOIN latest_message lm ON lm.session_id = s.id
	`

// ErrSessionNotFound reports that a session has no row in the
// session-list projection — either it does not exist, or it is one of
// the rows GetSessions drops after the query (a parentless subagent).
// Both cases mean the same thing to a caller holding a cached list:
// this session is not in it, so drop it.
var ErrSessionNotFound = errors.New("session not found in the session list")

// scanSessionRow scans one row of the sessionListQuery projection and
// applies the post-query derivations GetSessions performs in Go.
//
// keep is false for rows the session list drops after the query, so
// every caller of the projection filters identically. scan is
// rows.Scan or row.Scan.
func scanSessionRow(scan func(dest ...any) error) (s Session, keep bool, err error) {
	var parentID *string
	var lastRole, lastFinish, lastError *string
	var lastErrorName, lastErrorMessage *string
	var lastErrorAt *int64
	var lastSynthTerminal int
	err = scan(
		&s.ID, &s.ProjectID, &parentID, &s.Title, &s.Directory,
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
		return Session{}, false, err
	}
	s.ParentID = derefStr(parentID)
	s.DurationMs = s.TimeUpdated - s.TimeCreated

	// Provisional status from the last message. The owning adapter
	// re-settles it against the live turn signal (see
	// SettleSessionStatus) before anything user-visible reads it;
	// that is also where inactive children are filtered out, since
	// deciding that here would use the un-settled guess.
	role, finish, lastErr := derefStr(lastRole), derefStr(lastFinish), derefStr(lastError)
	s.Status = InferSessionStatus(role, finish, lastErr, lastSynthTerminal == 1)

	// Carry error metadata for the notice normalizer.
	s.LastErrorName = derefStr(lastErrorName)
	s.LastErrorMessage = derefStr(lastErrorMessage)
	if lastErrorAt != nil {
		s.LastErrorAt = *lastErrorAt
	}

	// Hide parentless sessions whose title marks them as a
	// subagent (e.g. "(auto-approve subagent)", "(@explore
	// subagent)"). These are created directly on the OpenCode
	// port so the parent_id check never catches them, yet
	// they should never surface as top-level rows. Subagents with
	// a real parent_id are handled above.
	if s.ParentID == "" && strings.HasSuffix(s.Title, " subagent)") {
		return s, false, nil
	}
	return s, true, nil
}

// GetSessionSummary recomputes exactly one row of the session list. It
// runs sessionListQuery — the same expressions GetSessions uses — with an
// id predicate, so the result is byte-identical to that session's row in
// a full scan. This is what lets the snapshot cache refresh a changed
// session without rescanning the whole database.
//
// Returns ErrSessionNotFound when the session has no row in the list.
func (d *DB) GetSessionSummary(ctx context.Context, sessionID string) (Session, error) {
	row := d.db.QueryRowContext(ctx, sessionListSelection+` WHERE s.id = ?`+sessionListProjection, sessionID)
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
	query += sessionListProjection + ` ORDER BY s.time_updated DESC`

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

// GetSessionTree returns the session containing sessionID, all of its native
// OpenCode ancestors, and every descendant of those ancestors. It uses the
// same projection as GetSessions so token and cost totals cannot drift.
func (d *DB) GetSessionTree(ctx context.Context, sessionID string) ([]Session, error) {
	query := `
		WITH RECURSIVE tree(id) AS (
			SELECT id FROM session WHERE id = ?
			UNION
			SELECT s.parent_id FROM session s JOIN tree t ON s.id = t.id
			WHERE s.parent_id IS NOT NULL
			UNION
			SELECT s.id FROM session s JOIN tree t ON s.parent_id = t.id
		), selected_sessions AS (
			SELECT * FROM session s WHERE s.id IN (SELECT id FROM tree)` +
		sessionListProjection + ` ORDER BY s.time_created, s.id`

	rows, err := d.db.QueryContext(ctx, query, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []Session
	for rows.Next() {
		session, keep, err := scanSessionRow(rows.Scan)
		if err != nil {
			return nil, err
		}
		if keep {
			sessions = append(sessions, session)
		}
	}
	return sessions, rows.Err()
}

// FilterInactiveChildren drops subagent sessions that are not currently
// running a turn. Active subagents are kept so the UI can nest them under
// their parent; finished ones have already bubbled their useful output up
// and only add noise to the session list.
//
// It keys off the platform's own parent id (OpenCode's session.parent_id),
// so it must run before ocman's MCP/worktree child links are stamped onto
// ParentID — those children are ordinary top-level sessions and must never
// be hidden. It must also run *after* SettleSessionStatus: filtering on the
// un-settled guess drops a child whose turn just started (its last message
// is still the user prompt, which infers as "done").
// The input slice is never mutated: callers may hold a cached, shared one.
func FilterInactiveChildren(sessions []Session) []Session {
	out := make([]Session, 0, len(sessions))
	for _, s := range sessions {
		if s.ParentID != "" && s.Status != StatusBusy {
			continue
		}
		out = append(out, s)
	}
	return out
}

// GetSession returns a single session by ID.
func (d *DB) GetSession(ctx context.Context, sessionID string) (*Session, error) {
	var s Session
	err := d.db.QueryRowContext(ctx, `
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

// GetSessionsInactiveBefore returns non-subagent sessions last updated before the cutoff.
func (d *DB) GetSessionsInactiveBefore(ctx context.Context, cutoff int64) ([]SessionArchiveCandidate, error) {
	rows, err := d.db.QueryContext(ctx, `
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

// MessageCountsSince returns, for each (sessionID, cutoff) pair in
// cutoffs, the number of messages in that session with
// time_created > cutoff. Sessions absent from the input map are
// absent from the output; sessions with zero unread are omitted to
// keep the response small.
//
// Implementation: a single CTE built from a VALUES clause batches
// every session into one query. The message table's covering index
// (session_id, time_created, id) makes each row a pure index range
// scan — no table reads, no JSON parsing. Measured at ~4 ms for
// 200 sessions on a 92k-message DB; see spec investigation in the
// PR for #70.
//
// Empty input returns an empty map and no error without touching
// the DB.
func (d *DB) MessageCountsSince(ctx context.Context, cutoffs map[string]int64) (map[string]int, error) {
	if len(cutoffs) == 0 {
		return map[string]int{}, nil
	}

	// Build a (?, ?), (?, ?), ... VALUES clause. We can't use a
	// table-valued subquery from Go-side bindings cleanly, so we
	// generate the placeholder list and bind the flattened args.
	var sb strings.Builder
	sb.WriteString(`
		WITH cutoffs(sid, cutoff) AS (VALUES `)
	args := make([]interface{}, 0, len(cutoffs)*2)
	first := true
	for sid, cutoff := range cutoffs {
		if !first {
			sb.WriteString(`,`)
		}
		sb.WriteString(`(?, ?)`)
		args = append(args, sid, cutoff)
		first = false
	}
	sb.WriteString(`)
		SELECT c.sid, COALESCE((
			SELECT count(*) FROM message m
			WHERE m.session_id = c.sid AND m.time_created > c.cutoff
		), 0) AS unread
		FROM cutoffs c
	`)

	rows, err := d.db.QueryContext(ctx, sb.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]int, len(cutoffs))
	for rows.Next() {
		var sid string
		var unread int
		if err := rows.Scan(&sid, &unread); err != nil {
			log.WithError(err).Warn("failed to scan unread count row")
			continue
		}
		if unread > 0 {
			out[sid] = unread
		}
	}
	return out, rows.Err()
}

// GetSessionMessages returns all messages for a session.
func (d *DB) GetSessionMessages(ctx context.Context, sessionID string) ([]Message, error) {
	rows, err := d.db.QueryContext(ctx, `
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
func (d *DB) GetSessionParts(ctx context.Context, sessionID string) ([]Part, error) {
	rows, err := d.db.QueryContext(ctx, `
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
func (d *DB) GetSessionDefaults(ctx context.Context, sessionID, directory string) (SessionDefaults, error) {
	if defaults, ok, err := d.lookupSessionDefaults(ctx, sessionID, directory); err != nil {
		return SessionDefaults{}, err
	} else if ok {
		return defaults, nil
	}
	// Fall back to "most recent across all directories" when the
	// directory has no qualifying sessions yet.
	defaults, _, err := d.lookupSessionDefaults(ctx, sessionID, "")
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
func (d *DB) lookupSessionDefaults(ctx context.Context, sessionID, directory string) (SessionDefaults, bool, error) {
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
	err := d.db.QueryRowContext(ctx, cte+sessionDefaultsTail, args...).
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
func (d *DB) GetContextTokenCount(ctx context.Context, sessionID string) (int64, error) {
	var count int64
	err := d.db.QueryRowContext(ctx, `
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
