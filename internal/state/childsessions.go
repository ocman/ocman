package state

import (
	"database/sql"
	"fmt"
)

const (
	ChildResultAsyncPending  = "async_pending"
	ChildResultAsyncQueueing = "async_queueing"
	ChildResultAsyncSending  = "async_sending"
	ChildResultWaitSending   = "wait_sending"
)

// ChildSession holds the data for one MCP-spawned child session.
type ChildSession struct {
	ID              string `json:"id"`
	Platform        string `json:"platform"`
	ParentSessionID string `json:"parentSessionID"`
	Intent          string `json:"intent"`
	ComposedPrompt  string `json:"composedPrompt,omitempty"`
	WorktreePath    string `json:"worktreePath,omitempty"` // empty for split_to_session
	Branch          string `json:"branch,omitempty"`       // empty for split_to_session
	TmuxTarget      string `json:"tmuxTarget,omitempty"`   // tmux session or session:window
	Status          string `json:"status"`                 // starting, sending, running, completed, error, cancelled
	CreatedAt       int64  `json:"createdAt"`
	CompletedAt     int64  `json:"completedAt"`       // 0 until terminal state
	Summary         string `json:"summary,omitempty"` // populated on completion
	ResultDelivery  string `json:"resultDelivery"`    // detached (legacy), async_pending, async_queueing, waiting, disconnected, delivered
}

// InsertChildSession persists a new child session record. The initial
// status is always "starting"; callers update it via UpdateChildSession.
func (d *DB) InsertChildSession(ctx context.Context, cs ChildSession) error {
	if cs.ResultDelivery == "" {
		cs.ResultDelivery = "detached"
	}
	_, err := d.db.ExecContext(ctx, `
		INSERT INTO child_sessions
			(id, platform, parent_session_id, intent, composed_prompt,
			 worktree_path, branch, tmux_target, status, created_at, result_delivery)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		cs.ID, cs.Platform, cs.ParentSessionID, cs.Intent, cs.ComposedPrompt,
		nullableString(cs.WorktreePath), nullableString(cs.Branch),
		nullableString(cs.TmuxTarget), cs.Status, cs.CreatedAt, cs.ResultDelivery,
	)
	if err != nil {
		return fmt.Errorf("inserting child session: %w", err)
	}
	return nil
}

// UpdateChildSession updates the mutable fields of a child session
// (status, completed_at, summary). Only the fields that are non-zero
// are updated; callers set CompletedAt and Summary when transitioning
// to a terminal state.
func (d *DB) UpdateChildSession(ctx context.Context, id, status, summary string, completedAt int64) error {
	_, err := d.db.ExecContext(ctx, `
		UPDATE child_sessions
		SET status       = ?,
		    summary      = CASE WHEN ? != '' THEN ? ELSE summary END,
		    completed_at = CASE WHEN ? != 0  THEN ? ELSE completed_at END
		WHERE id = ?
	`, status, summary, summary, completedAt, completedAt, id)
	if err != nil {
		return fmt.Errorf("updating child session: %w", err)
	}
	return nil
}

// ReopenChildSession marks a completed child as awaiting its next turn after
// its parent sends a follow-up.
func (d *DB) ClaimChildFollowup(id, previousDelivery, sendingDelivery string) (bool, error) {
	result, err := d.db.Exec(`
		UPDATE child_sessions
		SET status = 'sending', result_delivery = ?
		WHERE id = ? AND result_delivery = ?
	`, sendingDelivery, id, previousDelivery)
	if err != nil {
		return false, fmt.Errorf("claiming child follow-up: %w", err)
	}
	changed, err := result.RowsAffected()
	return changed == 1, err
}

func (d *DB) CompleteChildFollowup(id, sendingDelivery, delivery string) (bool, error) {
	result, err := d.db.Exec(`
		UPDATE child_sessions
		SET status = CASE WHEN status = 'sending' THEN 'running' ELSE status END,
		    summary = CASE WHEN status = 'sending' THEN NULL ELSE summary END,
		    completed_at = CASE WHEN status = 'sending' THEN NULL ELSE completed_at END,
		    result_delivery = ?
		WHERE id = ? AND result_delivery = ?
	`, delivery, id, sendingDelivery)
	if err != nil {
		return false, fmt.Errorf("completing child follow-up: %w", err)
	}
	changed, err := result.RowsAffected()
	return changed == 1, err
}

func (d *DB) RestoreChildFollowup(id string, cs ChildSession, sendingDelivery string) error {
	_, err := d.db.Exec(`
		UPDATE child_sessions
		SET status = ?, result_delivery = ?
		WHERE id = ? AND status = 'sending' AND result_delivery = ?
	`, cs.Status, cs.ResultDelivery, id, sendingDelivery)
	if err != nil {
		return fmt.Errorf("restoring child follow-up: %w", err)
	}
	return nil
}

// UpdateChildSessionCreatedAt backdates a child session's creation time.
// Used to age a row past the watcher's orphan grace period.
func (d *DB) UpdateChildSessionCreatedAt(ctx context.Context, id string, createdAt int64) error {
	_, err := d.db.ExecContext(ctx, `UPDATE child_sessions SET created_at = ? WHERE id = ?`, createdAt, id)
	if err != nil {
		return fmt.Errorf("updating child session created_at: %w", err)
	}
	return nil
}

// GetChildSession returns a single child session by ID, or an error
// wrapping sql.ErrNoRows when not found.
func (d *DB) GetChildSession(ctx context.Context, id string) (*ChildSession, error) {
	var cs ChildSession
	var worktreePath, branch, tmuxTarget, summary sql.NullString
	var completedAt sql.NullInt64
	err := d.db.QueryRowContext(ctx, `
		SELECT id, platform, parent_session_id, intent, composed_prompt,
		       worktree_path, branch, tmux_target, status,
		       created_at, completed_at, summary, result_delivery
		FROM child_sessions WHERE id = ?
	`, id).Scan(
		&cs.ID, &cs.Platform, &cs.ParentSessionID, &cs.Intent, &cs.ComposedPrompt,
		&worktreePath, &branch, &tmuxTarget, &cs.Status,
		&cs.CreatedAt, &completedAt, &summary, &cs.ResultDelivery,
	)
	if err != nil {
		return nil, fmt.Errorf("getting child session: %w", err)
	}
	cs.WorktreePath = worktreePath.String
	cs.Branch = branch.String
	cs.TmuxTarget = tmuxTarget.String
	cs.Summary = summary.String
	if completedAt.Valid {
		cs.CompletedAt = completedAt.Int64
	}
	return &cs, nil
}

// ListChildSessionsByParent returns all child sessions for the given
// parent session ID, ordered by created_at descending (newest first).
func (d *DB) ListChildSessionsByParent(ctx context.Context, parentSessionID string) ([]ChildSession, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT id, platform, parent_session_id, intent, composed_prompt,
		       worktree_path, branch, tmux_target, status,
		       created_at, completed_at, summary, result_delivery
		FROM child_sessions
		WHERE parent_session_id = ?
		ORDER BY created_at DESC
	`, parentSessionID)
	if err != nil {
		return nil, fmt.Errorf("listing child sessions: %w", err)
	}
	defer rows.Close()
	return scanChildSessions(rows)
}

// ListPendingChildSessions returns active children plus new async terminal
// results. Legacy "detached" rows are intentionally excluded.
func (d *DB) ListPendingChildSessions(ctx context.Context) ([]ChildSession, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT id, platform, parent_session_id, intent, composed_prompt,
		       worktree_path, branch, tmux_target, status,
		       created_at, completed_at, summary, result_delivery
		FROM child_sessions
		WHERE status IN ('starting', 'running', 'sending')
		   OR (status IN ('completed', 'error', 'cancelled') AND result_delivery IN ('async_sending', 'wait_sending', 'async_pending', 'async_queueing', 'waiting'))
		ORDER BY created_at ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("listing pending child sessions: %w", err)
	}
	defer rows.Close()
	return scanChildSessions(rows)
}

// CancelChildSession sets the status of a child session to "cancelled"
// and records the completion time. Idempotent: cancelling an already-
// terminal session is a no-op (returns nil).
func (d *DB) CancelChildSession(ctx context.Context, id string, cancelledAt int64) error {
	_, err := d.db.ExecContext(ctx, `
		UPDATE child_sessions
		SET status       = 'cancelled',
		    completed_at = ?
		WHERE id = ? AND status NOT IN ('completed', 'error', 'cancelled')
	`, cancelledAt, id)
	if err != nil {
		return fmt.Errorf("cancelling child session: %w", err)
	}
	return nil
}

func (d *DB) SetChildResultDelivery(ctx context.Context, id, delivery string) error {
	_, err := d.db.ExecContext(ctx, `UPDATE child_sessions SET result_delivery = ? WHERE id = ?`, delivery, id)
	if err != nil {
		return fmt.Errorf("updating child result delivery: %w", err)
	}
	return nil
}

func (d *DB) CompareAndSetChildResultDelivery(ctx context.Context, id, from, to string) (bool, error) {
	result, err := d.db.ExecContext(ctx, `UPDATE child_sessions SET result_delivery = ? WHERE id = ? AND result_delivery = ?`, to, id, from)
	if err != nil {
		return false, fmt.Errorf("claiming child result delivery: %w", err)
	}
	changed, err := result.RowsAffected()
	return changed == 1, err
}

func (d *DB) ListDisconnectedChildSessions(ctx context.Context, parentSessionID string) ([]ChildSession, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT id, platform, parent_session_id, intent, composed_prompt,
		       worktree_path, branch, tmux_target, status,
		       created_at, completed_at, summary, result_delivery
		FROM child_sessions
		WHERE parent_session_id = ? AND result_delivery = 'disconnected'
		ORDER BY created_at DESC
	`, parentSessionID)
	if err != nil {
		return nil, fmt.Errorf("listing disconnected child sessions: %w", err)
	}
	defer rows.Close()
	return scanChildSessions(rows)
}

// ChildSessionParents returns a map of every MCP-spawned child
// session's Key (platform + child session ID) to its parent session
// ID. Used by the server to overlay a parent link onto the listed
// sessions so the UI can nest a split child under the session that
// spawned it. Children whose parent session is not in the listing are
// still returned; the frontend promotes such orphans to top level.
func (d *DB) ChildSessionParents(ctx context.Context) (map[Key]string, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT platform, id, parent_session_id
		FROM child_sessions
		WHERE parent_session_id IS NOT NULL AND parent_session_id != ''
	`)
	if err != nil {
		return nil, fmt.Errorf("listing child session parents: %w", err)
	}
	defer rows.Close()
	out := map[Key]string{}
	for rows.Next() {
		var platform, id, parentID string
		if err := rows.Scan(&platform, &id, &parentID); err != nil {
			return nil, fmt.Errorf("scanning child session parent: %w", err)
		}
		out[Key{Platform: platform, SessionID: id}] = parentID
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading child session parents: %w", err)
	}
	return out, nil
}

// scanChildSessions scans a *sql.Rows result into a []ChildSession.
func scanChildSessions(rows *sql.Rows) ([]ChildSession, error) {
	var out []ChildSession
	for rows.Next() {
		var cs ChildSession
		var worktreePath, branch, tmuxTarget, summary sql.NullString
		var completedAt sql.NullInt64
		if err := rows.Scan(
			&cs.ID, &cs.Platform, &cs.ParentSessionID, &cs.Intent, &cs.ComposedPrompt,
			&worktreePath, &branch, &tmuxTarget, &cs.Status,
			&cs.CreatedAt, &completedAt, &summary, &cs.ResultDelivery,
		); err != nil {
			return nil, fmt.Errorf("scanning child session: %w", err)
		}
		cs.WorktreePath = worktreePath.String
		cs.Branch = branch.String
		cs.TmuxTarget = tmuxTarget.String
		cs.Summary = summary.String
		if completedAt.Valid {
			cs.CompletedAt = completedAt.Int64
		}
		out = append(out, cs)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading child sessions: %w", err)
	}
	return out, nil
}
