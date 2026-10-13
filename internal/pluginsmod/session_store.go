package pluginsmod

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/NoUseFreak/ocman/internal/state"
)

// migratePluginSession adds the session.v1 session mapping and its completed-turn
// outbox. plugin_session resolves a session back to the plugin that started
// it, so a turn completing after a restart still finds its recipient. The
// outbox mirrors plugin_conversation_outbox: a completed turn is durable work,
// not a best-effort call — id is AUTOINCREMENT (immutable identity and
// sequence), and the unique (plugin_id, operation_id) is what makes a repeated
// idle edge for the same turn a no-op rather than a second visible post.
func migratePluginSession(tx *sql.Tx) error {
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS plugin_session (
		platform_id TEXT NOT NULL,
		session_id TEXT NOT NULL,
		plugin_id TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		PRIMARY KEY(platform_id, session_id)
	)`); err != nil {
		return err
	}
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS plugin_session_outbox (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		plugin_id TEXT NOT NULL,
		platform_id TEXT NOT NULL,
		session_id TEXT NOT NULL,
		operation_id TEXT NOT NULL,
		text TEXT NOT NULL,
		status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','dead','done')),
		attempts INTEGER NOT NULL DEFAULT 0,
		next_attempt_at INTEGER NOT NULL DEFAULT 0,
		last_error TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		UNIQUE(plugin_id, operation_id)
	)`); err != nil {
		return err
	}
	_, err := tx.Exec(`CREATE INDEX IF NOT EXISTS plugin_session_outbox_group
		ON plugin_session_outbox(status, plugin_id, platform_id, session_id, id)`)
	return err
}

// PluginSessionCompletion is one durable completed turn awaiting
// acknowledgment. Group is its ordering group: completions for one session are
// strictly ordered by Sequence, and unrelated sessions never block each other.
type PluginSessionCompletion struct {
	ID         int64  `json:"id"`
	Sequence   int64  `json:"sequence"`
	PluginID   string `json:"pluginId"`
	PlatformID string `json:"platformId"`
	SessionID  string `json:"sessionId"`
	Text       string `json:"text"`
	Attempts   int    `json:"attempts"`
}

// Group keys the ordering group: one started session.
func (c PluginSessionCompletion) Group() string {
	return c.PluginID + "\x00" + c.PlatformID + "\x00" + c.SessionID
}

// LinkPluginSession records which plugin owns a session. Re-linking an existing
// session is not an error: the first claim stands.
func (d *Store) LinkPluginSession(ctx context.Context, platformID, sessionID, pluginID string) error {
	if platformID == "" || sessionID == "" || pluginID == "" {
		return state.ErrPluginInvalid
	}
	_, err := d.db.ExecContext(ctx, `INSERT INTO plugin_session (platform_id,session_id,plugin_id,created_at)
		VALUES(?,?,?,?) ON CONFLICT DO NOTHING`, platformID, sessionID, pluginID, time.Now().UnixMilli())
	if err != nil {
		return state.ErrPluginState
	}
	return nil
}

// GetPluginSessionPlugin is the reverse lookup a completed turn needs: it
// resolves a session back to the plugin that started it.
func (d *Store) GetPluginSessionPlugin(ctx context.Context, platformID, sessionID string) (string, bool, error) {
	if platformID == "" || sessionID == "" {
		return "", false, state.ErrPluginInvalid
	}
	var pluginID string
	err := d.db.QueryRowContext(ctx, `SELECT plugin_id FROM plugin_session WHERE platform_id=? AND session_id=?`, platformID, sessionID).Scan(&pluginID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, state.ErrPluginState
	}
	return pluginID, true, nil
}

// ListPluginSessionSessions returns the durable session identities that may owe
// a completed turn after an event edge was missed or the host restarted.
func (d *Store) ListPluginSessionSessions(ctx context.Context) ([]PluginSessionCompletion, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT DISTINCT platform_id,session_id,plugin_id FROM plugin_session`)
	if err != nil {
		return nil, state.ErrPluginState
	}
	defer rows.Close()
	var sessions []PluginSessionCompletion
	for rows.Next() {
		var s PluginSessionCompletion
		if err := rows.Scan(&s.PlatformID, &s.SessionID, &s.PluginID); err != nil {
			return nil, state.ErrPluginState
		}
		sessions = append(sessions, s)
	}
	if rows.Err() != nil {
		return nil, state.ErrPluginState
	}
	return sessions, nil
}

// HasPluginSessionCompletion includes pending, dead and delivered receipts, so
// recovery never re-reads a transcript whose turn is already durable.
func (d *Store) HasPluginSessionCompletion(ctx context.Context, pluginID, operationID string) (bool, error) {
	if pluginID == "" || operationID == "" {
		return false, state.ErrPluginInvalid
	}
	var exists bool
	err := d.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM plugin_session_outbox WHERE plugin_id=? AND operation_id=?)`, pluginID, operationID).Scan(&exists)
	if err != nil {
		return false, state.ErrPluginState
	}
	return exists, nil
}

// AppendPluginSessionCompletion records one completed turn for delivery and
// returns its immutable id. The insert is idempotent on (plugin, operation): a
// repeated idle edge for the same turn appends nothing and returns 0, which is
// what keeps a duplicate out of a plugin. An append over the caps is still
// accepted, because refusing a turn that already ran would lose it.
func (d *Store) AppendPluginSessionCompletion(ctx context.Context, pluginID, platformID, sessionID, operationID, text string) (int64, error) {
	if pluginID == "" || platformID == "" || sessionID == "" || operationID == "" || text == "" {
		return 0, state.ErrPluginInvalid
	}
	now := time.Now().UnixMilli()
	result, err := d.db.ExecContext(ctx, `INSERT INTO plugin_session_outbox
		(plugin_id,platform_id,session_id,operation_id,text,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?) ON CONFLICT DO NOTHING`,
		pluginID, platformID, sessionID, operationID, text, now, now)
	if err != nil {
		return 0, state.ErrPluginState
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0, state.ErrPluginState
	}
	if n != 1 {
		return 0, nil
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, state.ErrPluginState
	}
	return id, nil
}

// ClaimPluginSessionCompletions returns the head delivery of every ordering
// group due now, ordered by sequence. Only a group's head is returned: a turn
// may not overtake an earlier one in the same session, and a head that is dead
// or waiting out its backoff holds only its own session.
func (d *Store) ClaimPluginSessionCompletions(ctx context.Context, limit int) ([]PluginSessionCompletion, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := d.db.QueryContext(ctx, `SELECT id,plugin_id,platform_id,session_id,text,attempts
		FROM plugin_session_outbox o
		WHERE o.status='pending' AND o.next_attempt_at<=?
		  AND o.id=(SELECT MIN(h.id) FROM plugin_session_outbox h
			WHERE h.plugin_id=o.plugin_id AND h.platform_id=o.platform_id
			  AND h.session_id=o.session_id AND h.status IN ('pending','dead'))
		ORDER BY o.id LIMIT ?`, time.Now().UnixMilli(), limit)
	if err != nil {
		return nil, state.ErrPluginState
	}
	defer rows.Close()
	var claimed []PluginSessionCompletion
	for rows.Next() {
		var item PluginSessionCompletion
		if err := rows.Scan(&item.ID, &item.PluginID, &item.PlatformID, &item.SessionID, &item.Text, &item.Attempts); err != nil {
			return nil, state.ErrPluginState
		}
		item.Sequence = item.ID
		claimed = append(claimed, item)
	}
	if rows.Err() != nil {
		return nil, state.ErrPluginState
	}
	return claimed, nil
}

// PluginSessionCompletionCurrent reports whether a claimed delivery still
// matches its row: pending, due, and not attempted since the claim.
func (d *Store) PluginSessionCompletionCurrent(ctx context.Context, id int64, attempts int) (bool, error) {
	var n int
	err := d.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_session_outbox
		WHERE id=? AND status='pending' AND attempts=? AND next_attempt_at<=?`,
		id, attempts, time.Now().UnixMilli()).Scan(&n)
	if err != nil {
		return false, state.ErrPluginState
	}
	return n == 1, nil
}

// AckPluginSessionCompletion acknowledges a delivered turn. The row stays as a
// receipt so the same completed turn is never delivered again.
func (d *Store) AckPluginSessionCompletion(ctx context.Context, id int64) error {
	result, err := d.db.ExecContext(ctx, `UPDATE plugin_session_outbox
		SET status='done',last_error='',updated_at=? WHERE id=? AND status='pending'`,
		time.Now().UnixMilli(), id)
	if err != nil {
		return state.ErrPluginState
	}
	if n, err := result.RowsAffected(); err != nil {
		return state.ErrPluginState
	} else if n == 0 {
		return state.ErrPluginNotFound
	}
	return nil
}

// FailPluginSessionCompletion records one failed attempt. When attempts reach
// maxAttempts the delivery becomes a dead letter and its own session stops
// advancing rather than reordering around it.
//
// Session dead letters currently have no supported retry/discard controls.
// They retain owed work but lack conversation-outbox operator recovery parity;
// see docs/features/plugins.md for the delivery guarantee and limitation.
func (d *Store) FailPluginSessionCompletion(ctx context.Context, id int64, reason string, retryIn time.Duration, maxAttempts int) (bool, error) {
	now := time.Now()
	if len(reason) > 256 {
		reason = reason[:256]
	}
	result, err := d.db.ExecContext(ctx, `UPDATE plugin_session_outbox SET
		attempts=attempts+1,
		status=CASE WHEN attempts+1>=? THEN 'dead' ELSE 'pending' END,
		next_attempt_at=?,last_error=?,updated_at=?
		WHERE id=? AND status='pending'`,
		maxAttempts, now.Add(retryIn).UnixMilli(), reason, now.UnixMilli(), id)
	if err != nil {
		return false, state.ErrPluginState
	}
	if n, err := result.RowsAffected(); err != nil || n == 0 {
		return false, nil
	}
	var status string
	if err := d.db.QueryRowContext(ctx, `SELECT status FROM plugin_session_outbox WHERE id=?`, id).Scan(&status); err != nil {
		return false, state.ErrPluginState
	}
	return status == "dead", nil
}

// PluginSessionBacklogFull reports whether a plugin has hit a completion cap,
// the signal that pauses run admission. It fails closed: an unreadable backlog
// pauses rather than admitting work the host may not be able to deliver.
func (d *Store) PluginSessionBacklogFull(ctx context.Context, pluginID string) error {
	var rows int
	var bytes int64
	err := d.db.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(LENGTH(text)),0)
		FROM plugin_session_outbox WHERE plugin_id=? AND status IN ('pending','dead')`, pluginID).
		Scan(&rows, &bytes)
	if err != nil {
		return state.ErrPluginState
	}
	if rows >= state.PluginConversationOutboxMaxRows || bytes >= state.PluginConversationOutboxMaxBytes {
		return state.ErrPluginConversationBacklogFull
	}
	return nil
}

// PrunePluginSessionOutbox drops delivered receipts past their retention, but
// only once a later turn has superseded them, so the latest turn's receipt
// always survives reconciliation.
func (d *Store) PrunePluginSessionOutbox(ctx context.Context) error {
	const turn = `substr(%[1]s.operation_id, length(rtrim(%[1]s.operation_id, replace(%[1]s.operation_id, ':', ''))) + 1)`
	_, err := d.db.ExecContext(ctx, `DELETE FROM plugin_session_outbox
		WHERE status='done' AND updated_at<? AND EXISTS (
			SELECT 1 FROM plugin_session_outbox n
			WHERE n.plugin_id=plugin_session_outbox.plugin_id
			  AND n.platform_id=plugin_session_outbox.platform_id
			  AND n.session_id=plugin_session_outbox.session_id
			  AND n.id>plugin_session_outbox.id
			  AND `+fmt.Sprintf(turn, "n")+` <> `+fmt.Sprintf(turn, "plugin_session_outbox")+`)`,
		time.Now().Add(-sessionDoneRetention).UnixMilli())
	if err != nil {
		return state.ErrPluginState
	}
	return nil
}
