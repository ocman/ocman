package state

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// migrateToV97 adds the conversation reply outbox. A completed reply is durable
// work, not a best-effort call: it is written here before any provider send, so
// a disconnect, a plugin crash or a host restart replays it instead of losing
// it. id is AUTOINCREMENT, which makes it both the immutable identity and the
// monotonic sequence that orders delivery; ids are never reused.
//
// A row is never deleted on success, only marked done: the unique
// (plugin_id, operation_id) is what makes a repeated idle edge for the same
// completed turn a no-op rather than a second visible post.
func migrateToV97(tx *sql.Tx) error {
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS plugin_conversation_outbox (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		plugin_id TEXT NOT NULL,
		account_id TEXT NOT NULL,
		thread_id TEXT NOT NULL,
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
	_, err := tx.Exec(`CREATE INDEX IF NOT EXISTS plugin_conversation_outbox_group
		ON plugin_conversation_outbox(status, plugin_id, account_id, thread_id, id)`)
	return err
}

// Outbox limits. The count and byte caps are the visible pressure signal: when
// either is reached the host stops admitting new inbound conversation work
// instead of dropping replies it already owes.
const (
	// PluginConversationOutboxMaxRows bounds undelivered replies per plugin.
	PluginConversationOutboxMaxRows = 500
	// PluginConversationOutboxMaxBytes bounds their total reply text.
	PluginConversationOutboxMaxBytes = 8 << 20
	// pluginConversationDoneRetention keeps delivered receipts long enough to
	// suppress a late repeated idle edge, then prunes them so the table stays
	// bounded. A repeat after this window would post again.
	pluginConversationDoneRetention = 24 * time.Hour
)

// ErrPluginConversationBacklogFull reports that a plugin's outbox has reached
// its count or byte cap. Callers must stop producing new work, not drop it.
var ErrPluginConversationBacklogFull = errors.New("plugin conversation backlog is full")

// PluginConversationDelivery is one durable reply awaiting acknowledgment.
// Group is its ordering group: deliveries for one conversation are strictly
// ordered by Sequence, and unrelated conversations never block each other.
type PluginConversationDelivery struct {
	ID       int64 `json:"id"`
	Sequence int64 `json:"sequence"`
	Key      PluginConversationKey
	Text     string `json:"text"`
	Attempts int    `json:"attempts"`
}

// Group keys the ordering group: one provider conversation.
func (d PluginConversationDelivery) Group() string {
	return d.Key.PluginID + "\x00" + d.Key.AccountID + "\x00" + d.Key.ThreadID
}

// PluginConversationBacklog is the actionable delivery status for one plugin.
type PluginConversationBacklog struct {
	Pending      int                            `json:"pending"`
	Dead         int                            `json:"dead"`
	Bytes        int64                          `json:"bytes"`
	Retrying     int                            `json:"retrying"`
	Paused       bool                           `json:"paused"`
	MaxRows      int                            `json:"maxRows"`
	MaxBytes     int64                          `json:"maxBytes"`
	OldestUnsent int64                          `json:"oldestUnsent"`
	DeadLetters  []PluginConversationDeadLetter `json:"deadLetters"`
}

// PluginConversationDeadLetter is one reply that exhausted its retries and now
// waits for an explicit retry or discard.
type PluginConversationDeadLetter struct {
	ID        int64  `json:"id"`
	ThreadID  string `json:"threadId"`
	AccountID string `json:"accountId"`
	Attempts  int    `json:"attempts"`
	LastError string `json:"lastError"`
	UpdatedAt int64  `json:"updatedAt"`
	Bytes     int    `json:"bytes"`
}

// deadLetterListLimit bounds what the status endpoint returns. The counts stay
// exact; only the itemized list is capped.
const deadLetterListLimit = 50

// AppendPluginConversationReply records one completed reply for delivery and
// returns its immutable delivery id. The insert is idempotent on
// (plugin, operation): a repeated idle edge for the same turn appends nothing
// and returns 0, which is what keeps a duplicate out of a thread everyone can
// see.
//
// The caller is expected to have paused inbound work before the caps are hit;
// an append is still accepted over the cap, because refusing a reply for a turn
// that already ran would lose it. Overshoot is bounded by the turns in flight.
func (d *DB) AppendPluginConversationReply(ctx context.Context, key PluginConversationKey, operationID, text string) (int64, error) {
	if !key.valid() || operationID == "" || text == "" {
		return 0, ErrPluginInvalid
	}
	now := time.Now().UnixMilli()
	result, err := d.db.ExecContext(ctx, `INSERT INTO plugin_conversation_outbox
		(plugin_id,account_id,thread_id,operation_id,text,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?) ON CONFLICT DO NOTHING`,
		key.PluginID, key.AccountID, key.ThreadID, operationID, text, now, now)
	if err != nil {
		return 0, ErrPluginState
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0, ErrPluginState
	}
	if n != 1 {
		return 0, nil
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, ErrPluginState
	}
	return id, nil
}

// ClaimPluginConversationReplies returns the head delivery of every ordering
// group that is due now, newest groups included, ordered by sequence. Only a
// group's head is returned: a reply may not overtake an earlier one in the same
// conversation, and a head that is dead or waiting out its backoff holds only
// its own group. There is no lease: the host process owns this database, and an
// unacknowledged row is simply claimed again after a crash.
func (d *DB) ClaimPluginConversationReplies(ctx context.Context, limit int) ([]PluginConversationDelivery, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := d.db.QueryContext(ctx, `SELECT id,plugin_id,account_id,thread_id,text,attempts
		FROM plugin_conversation_outbox o
		WHERE o.status='pending' AND o.next_attempt_at<=?
		  AND o.id=(SELECT MIN(h.id) FROM plugin_conversation_outbox h
			WHERE h.plugin_id=o.plugin_id AND h.account_id=o.account_id
			  AND h.thread_id=o.thread_id AND h.status IN ('pending','dead'))
		ORDER BY o.id LIMIT ?`, time.Now().UnixMilli(), limit)
	if err != nil {
		return nil, ErrPluginState
	}
	defer rows.Close()
	var claimed []PluginConversationDelivery
	for rows.Next() {
		var item PluginConversationDelivery
		if err := rows.Scan(&item.ID, &item.Key.PluginID, &item.Key.AccountID, &item.Key.ThreadID,
			&item.Text, &item.Attempts); err != nil {
			return nil, ErrPluginState
		}
		item.Sequence = item.ID
		claimed = append(claimed, item)
	}
	if rows.Err() != nil {
		return nil, ErrPluginState
	}
	return claimed, nil
}

// AckPluginConversationReply acknowledges a delivered reply. The row stays as a
// receipt so the same completed turn is never posted again.
func (d *DB) AckPluginConversationReply(ctx context.Context, id int64) error {
	return d.updateOutbox(ctx, `UPDATE plugin_conversation_outbox
		SET status='done',last_error='',updated_at=? WHERE id=? AND status='pending'`,
		time.Now().UnixMilli(), id)
}

// FailPluginConversationReply records one failed attempt. retryIn is the
// bounded backoff before the next attempt; when attempts reach maxAttempts the
// delivery becomes a visible dead letter awaiting an explicit decision, and its
// conversation stops advancing rather than silently reordering around it.
func (d *DB) FailPluginConversationReply(ctx context.Context, id int64, reason string, retryIn time.Duration, maxAttempts int) (bool, error) {
	now := time.Now()
	if len(reason) > 256 {
		reason = reason[:256]
	}
	result, err := d.db.ExecContext(ctx, `UPDATE plugin_conversation_outbox SET
		attempts=attempts+1,
		status=CASE WHEN attempts+1>=? THEN 'dead' ELSE 'pending' END,
		next_attempt_at=?,last_error=?,updated_at=?
		WHERE id=? AND status='pending'`,
		maxAttempts, now.Add(retryIn).UnixMilli(), reason, now.UnixMilli(), id)
	if err != nil {
		return false, ErrPluginState
	}
	if n, err := result.RowsAffected(); err != nil || n == 0 {
		return false, nil
	}
	var status string
	if err := d.db.QueryRowContext(ctx, `SELECT status FROM plugin_conversation_outbox WHERE id=?`, id).Scan(&status); err != nil {
		return false, ErrPluginState
	}
	return status == "dead", nil
}

// RetryPluginConversationReply returns one dead letter to the pending head of
// its conversation, with its attempt count reset and no backoff left to wait.
func (d *DB) RetryPluginConversationReply(ctx context.Context, pluginID string, id int64) error {
	return d.updateOutbox(ctx, `UPDATE plugin_conversation_outbox
		SET status='pending',attempts=0,next_attempt_at=0,updated_at=?
		WHERE id=? AND plugin_id=? AND status='dead'`, time.Now().UnixMilli(), id, pluginID)
}

// DiscardPluginConversationReply drops one dead letter for good, unblocking its
// conversation. The row is kept as a receipt, so the discarded turn is not
// re-appended by a later idle edge.
func (d *DB) DiscardPluginConversationReply(ctx context.Context, pluginID string, id int64) error {
	return d.updateOutbox(ctx, `UPDATE plugin_conversation_outbox
		SET status='done',last_error='discarded',updated_at=?
		WHERE id=? AND plugin_id=? AND status='dead'`, time.Now().UnixMilli(), id, pluginID)
}

func (d *DB) updateOutbox(ctx context.Context, query string, args ...any) error {
	result, err := d.db.ExecContext(ctx, query, args...)
	if err != nil {
		return ErrPluginState
	}
	n, err := result.RowsAffected()
	if err != nil {
		return ErrPluginState
	}
	if n == 0 {
		return ErrPluginNotFound
	}
	return nil
}

// PluginConversationBacklogStatus reports one plugin's undelivered work. It is
// the single source for both the UI and the producer's pause decision, so what
// the user sees is what the host is enforcing.
func (d *DB) PluginConversationBacklogStatus(ctx context.Context, pluginID string) (PluginConversationBacklog, error) {
	if pluginID == "" {
		return PluginConversationBacklog{}, ErrPluginInvalid
	}
	status := PluginConversationBacklog{
		MaxRows: PluginConversationOutboxMaxRows, MaxBytes: PluginConversationOutboxMaxBytes,
	}
	var oldest sql.NullInt64
	err := d.db.QueryRowContext(ctx, `SELECT
		COALESCE(SUM(status='pending'),0), COALESCE(SUM(status='dead'),0),
		COALESCE(SUM(LENGTH(text)),0), COALESCE(SUM(status='pending' AND next_attempt_at>?),0),
		MIN(created_at)
		FROM plugin_conversation_outbox WHERE plugin_id=? AND status IN ('pending','dead')`,
		time.Now().UnixMilli(), pluginID).
		Scan(&status.Pending, &status.Dead, &status.Bytes, &status.Retrying, &oldest)
	if err != nil {
		return PluginConversationBacklog{}, ErrPluginState
	}
	status.OldestUnsent = oldest.Int64
	status.Paused = status.Pending+status.Dead >= PluginConversationOutboxMaxRows ||
		status.Bytes >= PluginConversationOutboxMaxBytes
	if status.Dead == 0 {
		return status, nil
	}
	rows, err := d.db.QueryContext(ctx, `SELECT id,account_id,thread_id,attempts,last_error,updated_at,LENGTH(text)
		FROM plugin_conversation_outbox WHERE plugin_id=? AND status='dead' ORDER BY id LIMIT ?`,
		pluginID, deadLetterListLimit)
	if err != nil {
		return PluginConversationBacklog{}, ErrPluginState
	}
	defer rows.Close()
	for rows.Next() {
		var item PluginConversationDeadLetter
		if err := rows.Scan(&item.ID, &item.AccountID, &item.ThreadID, &item.Attempts,
			&item.LastError, &item.UpdatedAt, &item.Bytes); err != nil {
			return PluginConversationBacklog{}, ErrPluginState
		}
		status.DeadLetters = append(status.DeadLetters, item)
	}
	if rows.Err() != nil {
		return PluginConversationBacklog{}, ErrPluginState
	}
	return status, nil
}

// PluginConversationBacklogFull reports whether a plugin has hit a cap, the
// signal that pauses inbound admission. It fails closed: a state error pauses
// rather than admitting work the host may not be able to answer.
func (d *DB) PluginConversationBacklogFull(ctx context.Context, pluginID string) error {
	var rows int
	var bytes int64
	err := d.db.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(LENGTH(text)),0)
		FROM plugin_conversation_outbox WHERE plugin_id=? AND status IN ('pending','dead')`, pluginID).
		Scan(&rows, &bytes)
	if err != nil {
		return ErrPluginState
	}
	if rows >= PluginConversationOutboxMaxRows || bytes >= PluginConversationOutboxMaxBytes {
		return ErrPluginConversationBacklogFull
	}
	return nil
}

// PrunePluginConversationOutbox drops delivered receipts past their retention.
func (d *DB) PrunePluginConversationOutbox(ctx context.Context) error {
	_, err := d.db.ExecContext(ctx, `DELETE FROM plugin_conversation_outbox
		WHERE status='done' AND updated_at<?`, time.Now().Add(-pluginConversationDoneRetention).UnixMilli())
	if err != nil {
		return ErrPluginState
	}
	return nil
}
