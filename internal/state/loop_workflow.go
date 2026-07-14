package state

import "fmt"

// LoopWorkflow identifies the workflow projection that executes a legacy
// loop. The loop id remains the public compatibility identifier.
type LoopWorkflow struct {
	LoopID     string
	WorkflowID string
	VersionID  string
	TriggerID  string
}

// GetLoopWorkflow returns the workflow projection for a legacy loop.
func (d *DB) GetLoopWorkflow(loopID string) (LoopWorkflow, error) {
	var m LoopWorkflow
	err := d.db.QueryRow(`SELECT loop_id, workflow_id, version_id, trigger_id FROM loop_workflow_map WHERE loop_id = ?`, loopID).
		Scan(&m.LoopID, &m.WorkflowID, &m.VersionID, &m.TriggerID)
	if err != nil {
		return LoopWorkflow{}, fmt.Errorf("getting loop workflow map: %w", err)
	}
	return m, nil
}

// EnsureLoopWorkflow creates the durable one-node workflow projection for a
// loop created after the original one-time migration. The loop row remains the
// compatibility read model for one release; scheduling is workflow-owned.
func (d *DB) EnsureLoopWorkflow(loopID string) error {
	var mapped int
	if err := d.db.QueryRow(`SELECT count(*) FROM loop_workflow_map WHERE loop_id = ?`, loopID).Scan(&mapped); err != nil {
		return fmt.Errorf("checking loop workflow map: %w", err)
	}
	if mapped != 0 {
		return nil
	}
	l, err := d.GetLoop(loopID)
	if err != nil {
		return err
	}
	tx, err := d.db.Begin()
	if err != nil {
		return fmt.Errorf("beginning loop workflow map: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	name := l.Title
	if name == "" {
		name = l.ID
	}
	if err := migrateLoopToWorkflow(tx, l.ID, name, l.Platform, l.RootSessionID, l.Directory, l.TriggerType,
		l.TriggerConfig, l.ActionType, l.ActionTemplate, l.StopConditions, l.State, l.CreatedAt, l.UpdatedAt,
		l.Model, l.Agent, l.Reasoning); err != nil {
		return err
	}
	return tx.Commit()
}
