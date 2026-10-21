package statev2

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

func refreshSidebar(ctx context.Context, tx *sql.Tx, key Key) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO session_sidebar `+sidebarSelect+`
	 WHERE s.instance_id=? AND s.id=? ON CONFLICT(instance_id,id) DO UPDATE SET
	 title=excluded.title,directory=excluded.directory,project_id=excluded.project_id,parent_id=excluded.parent_id,
	 created_at=excluded.created_at,updated_at=excluded.updated_at,last_message_at=excluded.last_message_at,
	 source_deleted=excluded.source_deleted,is_subagent=excluded.is_subagent,source_child=excluded.source_child,
	 project_directory=excluded.project_directory,inferred_status=excluded.inferred_status,
	 last_role=excluded.last_role,last_finish=excluded.last_finish,last_error=excluded.last_error`, key.InstanceID, key.SessionID)
	if err != nil {
		return err
	}
	return refreshSidebarAttention(ctx, tx, key)
}

type SidebarObservation struct {
	Status                             string
	ResetsAt                           int64
	PendingPermission, PendingQuestion bool
}

// BeginSidebarGeneration invalidates all observations from an earlier runtime.
func (d *DB) BeginSidebarGeneration(ctx context.Context, scope Instance, generation string) error {
	if scope.OwnerID == "" || scope.Platform == "" || scope.SourceKey == "" || generation == "" {
		return fmt.Errorf("sidebar: complete source and generation required")
	}
	_, err := d.db.ExecContext(ctx, `INSERT INTO sidebar_connection VALUES(?,?,?,?,0)
	 ON CONFLICT(owner_id,platform,source_key) DO UPDATE SET generation=excluded.generation,expires_at=0`, scope.OwnerID, scope.Platform, scope.SourceKey, generation)
	return err
}

// ReplaceSidebarObservations checkpoints event-cache state. The lease is
// renewed only by the current generation, never by a delayed old writer.
func (d *DB) ReplaceSidebarObservations(ctx context.Context, scope Instance, generation string, expiresAt int64, observations map[string]SidebarObservation) error {
	if observations == nil {
		observations = map[string]SidebarObservation{}
	}
	raw, err := json.Marshal(observations)
	if err != nil {
		return err
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `UPDATE sidebar_connection SET expires_at=? WHERE owner_id=? AND platform=? AND source_key=? AND generation=?`, expiresAt, scope.OwnerID, scope.Platform, scope.SourceKey, generation)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM sidebar_observation WHERE owner_id=? AND platform=? AND source_key=?
	 AND (generation!=? OR native_id NOT IN (SELECT key FROM json_each(?)))`, scope.OwnerID, scope.Platform, scope.SourceKey, generation, string(raw))
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO sidebar_observation
	 SELECT ?,?,?,key,?,json_extract(value,'$.Status'),COALESCE(json_extract(value,'$.ResetsAt'),0),json_extract(value,'$.PendingPermission'),json_extract(value,'$.PendingQuestion') FROM json_each(?) WHERE true
	 ON CONFLICT(owner_id,platform,source_key,native_id) DO UPDATE SET generation=excluded.generation,status=excluded.status,
	 resets_at=excluded.resets_at,pending_permission=excluded.pending_permission,pending_question=excluded.pending_question
	 WHERE generation!=excluded.generation OR status!=excluded.status OR resets_at!=excluded.resets_at OR pending_permission!=excluded.pending_permission OR pending_question!=excluded.pending_question`, scope.OwnerID, scope.Platform, scope.SourceKey, generation, string(raw))
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (d *DB) SetSidebarBranch(ctx context.Context, owner, directory, branch string, observedAt int64) error {
	if owner == "" || directory == "" {
		return fmt.Errorf("sidebar: owner and directory required")
	}
	_, err := d.db.ExecContext(ctx, `INSERT INTO sidebar_branch VALUES(?,?,?,?) ON CONFLICT(owner_id,directory) DO UPDATE SET branch=excluded.branch,observed_at=excluded.observed_at WHERE observed_at<=excluded.observed_at`, owner, directory, branch, observedAt)
	return err
}
