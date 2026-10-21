package statev2

import (
	"context"
	"database/sql"
)

const sidebarAttentionSelect = `SELECT s.instance_id,s.id,
 COALESCE(max(CASE WHEN m.role='assistant' THEN m.completed_at ELSE 0 END),0),
 COALESCE(max(CASE WHEN m.role='user' THEN m.created_at ELSE 0 END),0)
 FROM session s LEFT JOIN message m ON m.instance_id=s.instance_id AND m.session_id=s.id
 AND m.source_deleted=0 AND m.conversation_visible=1`

const sidebarAttentionSchema = `CREATE TABLE IF NOT EXISTS sidebar_attention (
 instance_id TEXT NOT NULL,session_id TEXT NOT NULL,last_turn_completed_at INTEGER NOT NULL,last_user_prompt_at INTEGER NOT NULL,
 PRIMARY KEY(instance_id,session_id),FOREIGN KEY(instance_id,session_id) REFERENCES session(instance_id,id) ON DELETE CASCADE
) WITHOUT ROWID;
INSERT OR REPLACE INTO sidebar_attention ` + sidebarAttentionSelect + ` GROUP BY s.instance_id,s.id;`

func refreshSidebarAttention(ctx context.Context, tx *sql.Tx, key Key) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO sidebar_attention `+sidebarAttentionSelect+`
 WHERE s.instance_id=? AND s.id=? GROUP BY s.instance_id,s.id
 ON CONFLICT(instance_id,session_id) DO UPDATE SET last_turn_completed_at=excluded.last_turn_completed_at,last_user_prompt_at=excluded.last_user_prompt_at`, key.InstanceID, key.SessionID)
	return err
}
