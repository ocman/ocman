package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// QueuedMessage is one follow-up prompt awaiting the session's next idle
// edge. images_json / model / agent / reasoning carry the send options so
// the flush reconstructs the original SendMessage faithfully.
type QueuedMessage struct {
	ID         string `json:"id"`
	Platform   string `json:"platform"`
	SessionID  string `json:"sessionID"`
	Position   int64  `json:"position"`
	Text       string `json:"text"`
	ImagesJSON string `json:"imagesJSON,omitempty"`
	Model      string `json:"model,omitempty"`
	Agent      string `json:"agent,omitempty"`
	Reasoning  string `json:"reasoning,omitempty"`
	CreatedAt  int64  `json:"createdAt"`
	// Attempts counts consecutive failed sends. Once it reaches
	// QueuedMessageAttemptLimit the row is Blocked: it stays visible with
	// LastError but is skipped by the drain so the rest of the queue moves.
	Attempts  int    `json:"attempts,omitempty"`
	LastError string `json:"lastError,omitempty"`
	Blocked   bool   `json:"blocked,omitempty"`
}

// QueuedMessageAttemptLimit is how many consecutive send failures a
// queued message tolerates before it is set aside. "opencode is
// restarting" recovers well within this; "the session was deleted"
// never will.
const QueuedMessageAttemptLimit = 5

// EnqueueMessage appends a queued message to the tail of its session's
// queue (position = current max + 1). The id is caller-supplied so the
// service can broadcast/track it.
func (d *DB) EnqueueMessage(ctx context.Context, m QueuedMessage) error {
	_, err := d.db.ExecContext(ctx, `
		INSERT INTO queued_message
			(id, platform, session_id, position, text, images_json,
			 model, agent, reasoning, created_at)
		VALUES (
			?, ?, ?,
			COALESCE((SELECT MAX(position) FROM queued_message
			          WHERE platform = ? AND session_id = ?), 0) + 1,
			?, ?, ?, ?, ?, ?)
	`,
		m.ID, m.Platform, m.SessionID,
		m.Platform, m.SessionID,
		m.Text, m.ImagesJSON, m.Model, m.Agent, m.Reasoning, m.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("enqueuing message: %w", err)
	}
	return nil
}

// CountQueuedMessages returns how many messages are queued for a session.
// Used by the enqueue path to decide whether a just-added message is the
// only one (so it may fast-path flush) vs. joining an existing backlog
// (which must wait for a real session.idle edge).
//
// A session's identity is (platform, sessionID) and the platform is matched
// exactly: an empty platform matches nothing. It used to wildcard, which let
// a session id shared by two machines pool into one queue.
func (d *DB) CountQueuedMessages(ctx context.Context, platform, sessionID string) (int, error) {
	var n int
	err := d.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM queued_message
		WHERE platform = ? AND session_id = ?
	`, platform, sessionID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("counting queued messages: %w", err)
	}
	return n, nil
}

// HeadQueuedMessage returns the lowest-position (oldest) queued message
// for a session, or (nil, nil) when the queue is empty. The platform is
// matched exactly — an empty platform matches nothing, so an idle edge that
// lost its platform can no longer pop a same-id session's head on another
// machine. Blocked rows are skipped so a dead-lettered message does not
// stall everything queued behind it.
func (d *DB) HeadQueuedMessage(ctx context.Context, platform, sessionID string) (*QueuedMessage, error) {
	var m QueuedMessage
	err := d.db.QueryRowContext(ctx, `
		SELECT id, platform, session_id, position, text, images_json,
		       model, agent, reasoning, created_at, attempts, last_error, blocked
		FROM queued_message
		WHERE platform = ? AND session_id = ? AND blocked = 0
		ORDER BY position ASC
		LIMIT 1
	`, platform, sessionID).Scan(
		&m.ID, &m.Platform, &m.SessionID, &m.Position, &m.Text, &m.ImagesJSON,
		&m.Model, &m.Agent, &m.Reasoning, &m.CreatedAt, &m.Attempts, &m.LastError, &m.Blocked,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading head queued message: %w", err)
	}
	return &m, nil
}

// QueuedSession identifies a session that has at least one pending
// queued message. Returned by SessionsWithQueuedMessages for the
// periodic drain sweep.
type QueuedSession struct {
	Platform  string
	SessionID string
}

// SessionsWithQueuedMessages returns every distinct (platform, session)
// that currently has at least one drainable queued message. The periodic
// sweep uses it to drain backlogs that never got a session.idle edge
// (e.g. rows stranded before a fix, or a swallowed edge).
//
// Archived sessions are excluded. Sending into one advances its
// time_updated past archived_at, which auto-unarchives it: the session
// the user abandoned pops back into the sidebar having burned tokens on
// a turn they walked away from. The predicate lives in the query so it
// cannot race the archive handler. Blocked rows are excluded too — a
// session whose queue is entirely dead-lettered has nothing to drain.
func (d *DB) SessionsWithQueuedMessages(ctx context.Context) ([]QueuedSession, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT DISTINCT q.platform, q.session_id
		FROM queued_message q
		WHERE q.blocked = 0
		  AND NOT EXISTS (
			SELECT 1 FROM archived_session a
			WHERE a.platform = q.platform AND a.session_id = q.session_id
		  )
	`)
	if err != nil {
		return nil, fmt.Errorf("listing sessions with queued messages: %w", err)
	}
	defer rows.Close()
	var out []QueuedSession
	for rows.Next() {
		var q QueuedSession
		if err := rows.Scan(&q.Platform, &q.SessionID); err != nil {
			return nil, fmt.Errorf("scanning queued session: %w", err)
		}
		out = append(out, q)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading queued sessions: %w", err)
	}
	return out, nil
}

// ListQueuedMessages returns a session's queue ordered oldest-first. The
// platform is matched exactly (see HeadQueuedMessage).
func (d *DB) ListQueuedMessages(ctx context.Context, platform, sessionID string) ([]QueuedMessage, error) {
	return d.queuedMessages(ctx, `
		SELECT id, platform, session_id, position, text, images_json,
		       model, agent, reasoning, created_at, attempts, last_error, blocked
		FROM queued_message
		WHERE platform = ? AND session_id = ?
		ORDER BY position ASC
	`, platform, sessionID)
}

// ListQueuedMessagesAnyPlatform lists a session's queue across every
// platform. It is the single deliberate cross-platform query left in this
// file, kept for the read-only GET /api/session/{id}/queue endpoint whose
// `platform` parameter is optional — an older client, or a hand-built URL,
// has only the bare session id. Nothing about delivery may use it: with two
// machines sharing a session id it returns both queues, which is acceptable
// for a display list and never for a drain.
func (d *DB) ListQueuedMessagesAnyPlatform(ctx context.Context, sessionID string) ([]QueuedMessage, error) {
	return d.queuedMessages(ctx, `
		SELECT id, platform, session_id, position, text, images_json,
		       model, agent, reasoning, created_at, attempts, last_error, blocked
		FROM queued_message
		WHERE session_id = ?
		ORDER BY platform ASC, position ASC
	`, sessionID)
}

func (d *DB) queuedMessages(ctx context.Context, query string, args ...any) ([]QueuedMessage, error) {
	rows, err := d.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing queued messages: %w", err)
	}
	defer rows.Close()
	var out []QueuedMessage
	for rows.Next() {
		var m QueuedMessage
		if err := rows.Scan(
			&m.ID, &m.Platform, &m.SessionID, &m.Position, &m.Text, &m.ImagesJSON,
			&m.Model, &m.Agent, &m.Reasoning, &m.CreatedAt, &m.Attempts, &m.LastError, &m.Blocked,
		); err != nil {
			return nil, fmt.Errorf("scanning queued message: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading queued messages: %w", err)
	}
	return out, nil
}

// RecordQueuedMessageFailure counts a failed send for a queued message
// and reports whether that failure exhausted its budget. An exhausted
// message is blocked: it stays in the queue (visible, with the reason)
// but is skipped by the drain, so the messages behind it are no longer
// stuck behind something that will never send.
func (d *DB) RecordQueuedMessageFailure(ctx context.Context, id, reason string) (blocked bool, err error) {
	res, err := d.db.ExecContext(ctx, `
		UPDATE queued_message
		SET attempts = attempts + 1,
		    last_error = ?,
		    blocked = CASE WHEN attempts + 1 >= ? THEN 1 ELSE 0 END
		WHERE id = ?
	`, reason, QueuedMessageAttemptLimit, id)
	if err != nil {
		return false, fmt.Errorf("recording queued message failure: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return false, nil
	}
	err = d.db.QueryRowContext(ctx, `SELECT blocked FROM queued_message WHERE id = ?`, id).Scan(&blocked)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("reading queued message block state: %w", err)
	}
	return blocked, nil
}

// ClearQueuedMessagesForSession drops a session's whole queue. Used when
// the user archives a session: the follow-ups they queued are part of the
// work being abandoned.
func (d *DB) ClearQueuedMessagesForSession(ctx context.Context, platform, sessionID string) (int64, error) {
	res, err := d.db.ExecContext(ctx,
		`DELETE FROM queued_message WHERE platform = ? AND session_id = ?`,
		platform, sessionID,
	)
	if err != nil {
		return 0, fmt.Errorf("clearing queued messages: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// DeleteQueuedMessage removes a single queued message by id. Returns
// whether a row was actually deleted.
func (d *DB) DeleteQueuedMessage(ctx context.Context, id string) (bool, error) {
	res, err := d.db.ExecContext(ctx, `DELETE FROM queued_message WHERE id = ?`, id)
	if err != nil {
		return false, fmt.Errorf("deleting queued message: %w", err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// MoveQueuedMessage reorders a message by swapping its position with the
// adjacent message in the given direction (-1 = earlier/up, +1 =
// later/down) within the same session. A no-op at a boundary (already
// first/last) returns (false, nil). The swap is transactional so a
// reorder never leaves two rows sharing a position.
func (d *DB) MoveQueuedMessage(ctx context.Context, id string, direction int) (bool, error) {
	if direction != -1 && direction != 1 {
		return false, fmt.Errorf("invalid direction %d", direction)
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("move queued message begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var platform, sessionID string
	var pos int64
	err = tx.QueryRowContext(ctx, `
		SELECT platform, session_id, position FROM queued_message WHERE id = ?
	`, id).Scan(&platform, &sessionID, &pos)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("move: loading message: %w", err)
	}

	// Find the neighbor to swap with: the closest row in the requested
	// direction within the same session.
	var neighborID string
	var neighborPos int64
	var q string
	if direction < 0 {
		q = `SELECT id, position FROM queued_message
		     WHERE platform = ? AND session_id = ? AND position < ?
		     ORDER BY position DESC LIMIT 1`
	} else {
		q = `SELECT id, position FROM queued_message
		     WHERE platform = ? AND session_id = ? AND position > ?
		     ORDER BY position ASC LIMIT 1`
	}
	err = tx.QueryRowContext(ctx, q, platform, sessionID, pos).Scan(&neighborID, &neighborPos)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil // already at the boundary
	}
	if err != nil {
		return false, fmt.Errorf("move: loading neighbor: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `UPDATE queued_message SET position = ? WHERE id = ?`, neighborPos, id); err != nil {
		return false, fmt.Errorf("move: updating message: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE queued_message SET position = ? WHERE id = ?`, pos, neighborID); err != nil {
		return false, fmt.Errorf("move: updating neighbor: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("move queued message commit: %w", err)
	}
	return true, nil
}

// GetQueuedMessageSession returns the (platform, session) a queued
// message belongs to, or ok=false when it doesn't exist. The handler
// uses it to confirm a delete/move targets the session in the URL before
// mutating (so one session can't reorder another's queue).
func (d *DB) GetQueuedMessageSession(ctx context.Context, id string) (platform, sessionID string, ok bool, err error) {
	err = d.db.QueryRowContext(ctx, `
		SELECT platform, session_id FROM queued_message WHERE id = ?
	`, id).Scan(&platform, &sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, fmt.Errorf("getting queued message session: %w", err)
	}
	return platform, sessionID, true, nil
}
