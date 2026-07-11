package state

import (
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
}

// EnqueueMessage appends a queued message to the tail of its session's
// queue (position = current max + 1). The id is caller-supplied so the
// service can broadcast/track it.
func (d *DB) EnqueueMessage(m QueuedMessage) error {
	_, err := d.db.Exec(`
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
// (which must wait for a real session.idle edge). An empty platform
// matches any platform.
func (d *DB) CountQueuedMessages(platform, sessionID string) (int, error) {
	var n int
	err := d.db.QueryRow(`
		SELECT COUNT(*) FROM queued_message
		WHERE (? = '' OR platform = ?) AND session_id = ?
	`, platform, platform, sessionID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("counting queued messages: %w", err)
	}
	return n, nil
}

// HeadQueuedMessage returns the lowest-position (oldest) queued message
// for a session, or (nil, nil) when the queue is empty. An empty platform
// matches any platform (the idle-driven flush knows only the session id;
// the head row carries the authoritative platform).
func (d *DB) HeadQueuedMessage(platform, sessionID string) (*QueuedMessage, error) {
	var m QueuedMessage
	err := d.db.QueryRow(`
		SELECT id, platform, session_id, position, text, images_json,
		       model, agent, reasoning, created_at
		FROM queued_message
		WHERE (? = '' OR platform = ?) AND session_id = ?
		ORDER BY position ASC
		LIMIT 1
	`, platform, platform, sessionID).Scan(
		&m.ID, &m.Platform, &m.SessionID, &m.Position, &m.Text, &m.ImagesJSON,
		&m.Model, &m.Agent, &m.Reasoning, &m.CreatedAt,
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
// that currently has at least one queued message. The periodic sweep
// uses it to drain backlogs that never got a session.idle edge (e.g.
// rows stranded before a fix, or a swallowed edge).
func (d *DB) SessionsWithQueuedMessages() ([]QueuedSession, error) {
	rows, err := d.db.Query(`
		SELECT DISTINCT platform, session_id FROM queued_message
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

// ListQueuedMessages returns a session's queue ordered oldest-first.
func (d *DB) ListQueuedMessages(platform, sessionID string) ([]QueuedMessage, error) {
	// An empty platform matches any platform (wildcard), mirroring
	// HeadQueuedMessage — so callers that only know the session id (the
	// idle-edge flush, the queue.updated broadcast) resolve it.
	rows, err := d.db.Query(`
		SELECT id, platform, session_id, position, text, images_json,
		       model, agent, reasoning, created_at
		FROM queued_message
		WHERE (? = '' OR platform = ?) AND session_id = ?
		ORDER BY position ASC
	`, platform, platform, sessionID)
	if err != nil {
		return nil, fmt.Errorf("listing queued messages: %w", err)
	}
	defer rows.Close()
	var out []QueuedMessage
	for rows.Next() {
		var m QueuedMessage
		if err := rows.Scan(
			&m.ID, &m.Platform, &m.SessionID, &m.Position, &m.Text, &m.ImagesJSON,
			&m.Model, &m.Agent, &m.Reasoning, &m.CreatedAt,
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

// DeleteQueuedMessage removes a single queued message by id. Returns
// whether a row was actually deleted.
func (d *DB) DeleteQueuedMessage(id string) (bool, error) {
	res, err := d.db.Exec(`DELETE FROM queued_message WHERE id = ?`, id)
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
func (d *DB) MoveQueuedMessage(id string, direction int) (bool, error) {
	if direction != -1 && direction != 1 {
		return false, fmt.Errorf("invalid direction %d", direction)
	}
	tx, err := d.db.Begin()
	if err != nil {
		return false, fmt.Errorf("move queued message begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var platform, sessionID string
	var pos int64
	err = tx.QueryRow(`
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
	err = tx.QueryRow(q, platform, sessionID, pos).Scan(&neighborID, &neighborPos)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil // already at the boundary
	}
	if err != nil {
		return false, fmt.Errorf("move: loading neighbor: %w", err)
	}

	if _, err := tx.Exec(`UPDATE queued_message SET position = ? WHERE id = ?`, neighborPos, id); err != nil {
		return false, fmt.Errorf("move: updating message: %w", err)
	}
	if _, err := tx.Exec(`UPDATE queued_message SET position = ? WHERE id = ?`, pos, neighborID); err != nil {
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
func (d *DB) GetQueuedMessageSession(id string) (platform, sessionID string, ok bool, err error) {
	err = d.db.QueryRow(`
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
