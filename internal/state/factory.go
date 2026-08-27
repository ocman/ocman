package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory"
)

// UpsertFactoryLocalExecutionAck records the operator's acknowledgement for
// one host, repository, and permission profile tuple.
func (d *DB) UpsertFactoryLocalExecutionAck(ctx context.Context, hostID, repoRoot, profileID, profileVersion, actor string, at time.Time) error {
	_, err := d.db.ExecContext(ctx, `
		INSERT INTO factory_local_execution_ack
			(host_id, repo_root, profile_id, profile_version, acknowledged_by, acknowledged_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(host_id, repo_root, profile_id, profile_version) DO UPDATE SET
			acknowledged_by = excluded.acknowledged_by,
			acknowledged_at = excluded.acknowledged_at
	`, hostID, repoRoot, profileID, profileVersion, actor, at.UnixMilli())
	if err != nil {
		return fmt.Errorf("upserting Factory local execution acknowledgement: %w", err)
	}
	return nil
}

// GetFactoryPlanningSession returns the durable session bound to one Planning Work item.
func (d *DB) GetFactoryPlanningSession(ctx context.Context, workID string) (factory.PlanningSession, bool, error) {
	var session factory.PlanningSession
	var metadata string
	err := d.db.QueryRowContext(ctx, `
		SELECT external_id, metadata_json
		FROM factory_external_mapping
		WHERE system = 'session' AND external_kind = 'planning' AND entity_kind = 'planning_work' AND entity_id = ?
	`, workID).Scan(&session.ID, &metadata)
	if err == sql.ErrNoRows {
		return factory.PlanningSession{}, false, nil
	}
	if err != nil {
		return factory.PlanningSession{}, false, fmt.Errorf("getting Factory Planning Session: %w", err)
	}
	var stored struct {
		Platform string `json:"platform"`
	}
	if err := json.Unmarshal([]byte(metadata), &stored); err != nil || stored.Platform == "" {
		return factory.PlanningSession{}, false, errors.New("getting Factory Planning Session: invalid metadata")
	}
	session.Platform = stored.Platform
	return session, true, nil
}

// PutFactoryPlanningSession binds one Planning Work item to exactly one session.
func (d *DB) PutFactoryPlanningSession(ctx context.Context, epicID, workID string, session factory.PlanningSession) error {
	metadata, err := json.Marshal(map[string]string{"epicId": epicID, "platform": session.Platform})
	if err != nil {
		return err
	}
	_, err = d.db.ExecContext(ctx, `
		INSERT INTO factory_external_mapping
			(system, external_kind, external_id, entity_kind, entity_id, metadata_json, created_at)
		VALUES ('session', 'planning', ?, 'planning_work', ?, ?, ?)
		ON CONFLICT(system, external_kind, entity_kind, entity_id) DO NOTHING
	`, session.ID, workID, string(metadata), time.Now().UnixMilli())
	if err != nil {
		return fmt.Errorf("putting Factory Planning Session: %w", err)
	}
	stored, ok, err := d.GetFactoryPlanningSession(ctx, workID)
	if err != nil {
		return err
	}
	if !ok || stored != session {
		return errors.New("putting Factory Planning Session: Planning Work is already bound to another session")
	}
	return nil
}

// AppendFactoryAudit records an immutable Factory decision or graph transition.
func (d *DB) AppendFactoryAudit(ctx context.Context, record factory.FactoryAuditRecord) error {
	details, err := json.Marshal(record.Details)
	if err != nil {
		return fmt.Errorf("encoding Factory audit: %w", err)
	}
	_, err = d.db.ExecContext(ctx, `
		INSERT INTO factory_audit_record (epic_id, work_item_id, actor, action, details_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, record.EpicID, record.WorkID, record.Actor, record.Action, string(details), record.At.UnixMilli())
	if err != nil {
		return fmt.Errorf("appending Factory audit: %w", err)
	}
	return nil
}
