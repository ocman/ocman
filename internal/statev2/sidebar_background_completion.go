package statev2

import (
	"context"
	"database/sql"
	"time"
)

// Completion survives observation expiry and runtime generations. Its identity
// stays owner/source scoped, like the retained conversation it orders.
const sidebarBackgroundCompletionSchema = `CREATE TABLE IF NOT EXISTS sidebar_background_completion (
 owner_id TEXT NOT NULL,platform TEXT NOT NULL,source_key TEXT NOT NULL,native_id TEXT NOT NULL,
 completed_at INTEGER NOT NULL,
 PRIMARY KEY(owner_id,platform,source_key,native_id)
) WITHOUT ROWID;`

// Checkpoint the final finite task edge before replacing the old observations.
// A repeated snapshot or a disconnected source must not invent a completion.
func checkpointBackgroundCompletion(ctx context.Context, tx *sql.Tx, scope Instance, generation, observations string, available bool) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO sidebar_background_completion
	 SELECT owner_id,platform,source_key,native_id,? FROM sidebar_observation o
	 WHERE owner_id=? AND platform=? AND source_key=? AND generation=? AND ?
	 AND background_tasks>background_services
	 AND NOT EXISTS (SELECT 1 FROM json_each(?) n WHERE n.key=o.native_id
	  AND COALESCE(json_extract(n.value,'$.BackgroundTasks'),0)>COALESCE(json_extract(n.value,'$.BackgroundServices'),0))
	 ON CONFLICT(owner_id,platform,source_key,native_id) DO UPDATE SET completed_at=excluded.completed_at`,
		time.Now().UnixMilli(), scope.OwnerID, scope.Platform, scope.SourceKey, generation, available, observations)
	return err
}
