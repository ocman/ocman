package state

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// migrateToV96 persists the conversation thread to session mapping. The key is
// owner-qualified through platform_id, which carries the remote owner for a
// session that lives on another machine (r-<remote>:opencode), so the same
// bare session id on two machines maps to two distinct threads.
func migrateToV96(tx *sql.Tx) error {
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS plugin_conversation (
		plugin_id TEXT NOT NULL,
		account_id TEXT NOT NULL,
		thread_id TEXT NOT NULL,
		platform_id TEXT NOT NULL,
		session_id TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		PRIMARY KEY(plugin_id, account_id, thread_id)
	)`); err != nil {
		return err
	}
	_, err := tx.Exec(`CREATE INDEX IF NOT EXISTS plugin_conversation_session
		ON plugin_conversation(platform_id, session_id)`)
	return err
}

// PluginConversationKey identifies one provider conversation. The account is
// part of the key, not decoration: two workspaces can hand out the same thread
// identity, and a mapping keyed on the thread alone would merge them.
type PluginConversationKey struct {
	PluginID  string `json:"pluginId"`
	AccountID string `json:"accountId"`
	ThreadID  string `json:"threadId"`
}

// PluginConversationSession is the mapped session's full identity. A bare
// session id is not an identity across machines.
type PluginConversationSession struct {
	PlatformID string `json:"platformId"`
	SessionID  string `json:"sessionId"`
}

func (k PluginConversationKey) valid() bool {
	return k.PluginID != "" && k.AccountID != "" && k.ThreadID != ""
}

// LinkPluginConversation claims the mapping for a conversation, returning the
// mapping that is now authoritative and whether this caller's session won the
// claim. The insert-if-absent is the atomic arbiter: two concurrent first
// messages both reach it, exactly one wins, and the loser is told to use the
// winner's session instead of mapping a second one.
func (d *DB) LinkPluginConversation(ctx context.Context, key PluginConversationKey, session PluginConversationSession) (PluginConversationSession, bool, error) {
	if !key.valid() || session.PlatformID == "" || session.SessionID == "" {
		return PluginConversationSession{}, false, ErrPluginInvalid
	}
	result, err := d.db.ExecContext(ctx, `INSERT INTO plugin_conversation
		(plugin_id,account_id,thread_id,platform_id,session_id,created_at) VALUES(?,?,?,?,?,?)
		ON CONFLICT DO NOTHING`,
		key.PluginID, key.AccountID, key.ThreadID, session.PlatformID, session.SessionID, time.Now().UnixMilli())
	if err != nil {
		return PluginConversationSession{}, false, ErrPluginState
	}
	n, err := result.RowsAffected()
	if err != nil {
		return PluginConversationSession{}, false, ErrPluginState
	}
	if n == 1 {
		return session, true, nil
	}
	existing, ok, err := d.GetPluginConversation(ctx, key)
	if err != nil {
		return PluginConversationSession{}, false, err
	}
	if !ok {
		// Unlinked between the conflict and the read; the caller retries.
		return PluginConversationSession{}, false, ErrPluginNotFound
	}
	return existing, false, nil
}

func (d *DB) GetPluginConversation(ctx context.Context, key PluginConversationKey) (PluginConversationSession, bool, error) {
	if !key.valid() {
		return PluginConversationSession{}, false, ErrPluginInvalid
	}
	var session PluginConversationSession
	err := d.db.QueryRowContext(ctx, `SELECT platform_id,session_id FROM plugin_conversation
		WHERE plugin_id=? AND account_id=? AND thread_id=?`, key.PluginID, key.AccountID, key.ThreadID).
		Scan(&session.PlatformID, &session.SessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return PluginConversationSession{}, false, nil
	}
	if err != nil {
		return PluginConversationSession{}, false, ErrPluginState
	}
	return session, true, nil
}

// GetPluginConversationThread is the reverse lookup a completed turn needs: it
// resolves a session back to the conversation that owns it. Reading it from the
// database is what lets a reply reach the thread after a restart.
func (d *DB) GetPluginConversationThread(ctx context.Context, session PluginConversationSession) (PluginConversationKey, bool, error) {
	if session.PlatformID == "" || session.SessionID == "" {
		return PluginConversationKey{}, false, ErrPluginInvalid
	}
	var key PluginConversationKey
	err := d.db.QueryRowContext(ctx, `SELECT plugin_id,account_id,thread_id FROM plugin_conversation
		WHERE platform_id=? AND session_id=?`, session.PlatformID, session.SessionID).
		Scan(&key.PluginID, &key.AccountID, &key.ThreadID)
	if errors.Is(err, sql.ErrNoRows) {
		return PluginConversationKey{}, false, nil
	}
	if err != nil {
		return PluginConversationKey{}, false, ErrPluginState
	}
	return key, true, nil
}
