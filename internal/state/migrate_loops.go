package state

import (
	"database/sql"
	"encoding/json"
	"fmt"
)

// migrateToV28 performs the one-time loop → one-node workflow copy (#325).
//
// Each persisted (non-deleted) loop is converted into a one-node workflow
// definition + version carrying the corresponding trigger and the loop's
// preserved policies (under definition_json ".loopCompat"). Each loop
// iteration becomes a historical workflow run with one node run + node
// attempt, mapping the iteration outcome to run/attempt state as faithfully
// as the stored data permits. `loop_workflow_map` records the loop→workflow
// id mapping so existing loop identifiers stay resolvable.
//
// The copy is idempotent (loops already in `loop_workflow_map` are skipped)
// and interrupted-safe (it runs inside the migration transaction, so a crash
// rolls the whole step back and a re-run starts clean). The original `loops`
// / `loop_iterations` tables are intentionally left untouched: the existing
// loop REST/MCP/UI surfaces remain compatibility wrappers over them for one
// release (see docs/architecture.md and the loop→workflow removal note).
func migrateToV28(tx *sql.Tx) error {
	// Defense-in-depth for the pre-merge single-feature v26 branches: ensure
	// the artifact and resource-lease objects exist before the loop copy runs,
	// in case a dev DB reached this step via a version ordering that skipped
	// them (see the migrateToV26 note). All ensure* helpers are idempotent.
	if err := ensureWorkflowArtifactSchema(tx); err != nil {
		return err
	}
	if err := ensureWorkflowResourceLeaseSchema(tx); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS loop_workflow_map (
			loop_id     TEXT PRIMARY KEY,
			workflow_id TEXT NOT NULL,
			version_id  TEXT NOT NULL,
			trigger_id  TEXT NOT NULL,
			migrated_at INTEGER NOT NULL
		)
	`); err != nil {
		return fmt.Errorf("creating loop_workflow_map: %w", err)
	}

	// The loops tables may not exist on a database whose loops migrations
	// were somehow absent; guard so a fresh/odd DB still migrates.
	var loopsTable int
	if err := tx.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='loops'`).Scan(&loopsTable); err != nil {
		return fmt.Errorf("probing for loops table: %w", err)
	}
	if loopsTable == 0 {
		return nil
	}

	rows, err := tx.Query(`
		SELECT id, CASE WHEN title != '' THEN title ELSE id END,
		       directory, trigger_type, trigger_config, action_type,
		       action_template, stop_conditions, state, created_at, updated_at
		FROM loops
		WHERE state != 'deleted'
		  AND id NOT IN (SELECT loop_id FROM loop_workflow_map)
		ORDER BY created_at, id`)
	if err != nil {
		return fmt.Errorf("listing loops to migrate: %w", err)
	}
	type loopRow struct {
		id, name, directory                    string
		triggerType, triggerConfig, actionType string
		actionTemplate, stopConditions, state  string
		createdAt, updatedAt                   int64
	}
	var loopsToMigrate []loopRow
	for rows.Next() {
		var l loopRow
		if err := rows.Scan(&l.id, &l.name, &l.directory, &l.triggerType, &l.triggerConfig,
			&l.actionType, &l.actionTemplate, &l.stopConditions, &l.state,
			&l.createdAt, &l.updatedAt); err != nil {
			rows.Close()
			return fmt.Errorf("scanning loop: %w", err)
		}
		loopsToMigrate = append(loopsToMigrate, l)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("reading loops: %w", err)
	}

	for _, l := range loopsToMigrate {
		if err := migrateLoopToWorkflow(tx, l.id, l.name, l.directory, l.triggerType,
			l.triggerConfig, l.actionType, l.actionTemplate, l.stopConditions,
			l.state, l.createdAt, l.updatedAt); err != nil {
			return fmt.Errorf("migrating loop %s: %w", l.id, err)
		}
	}
	return nil
}

func migrateLoopToWorkflow(tx *sql.Tx, loopID, name, directory, triggerType, triggerConfig,
	actionType, actionTemplate, stopConditions, loopState string, createdAt, updatedAt int64) error {
	workflowID := "wf_loop_" + loopID
	versionID := "wfv_loop_" + loopID
	nodeID := "action"
	triggerID := "trigger"

	trigger := loopTriggerToWorkflow(triggerType, triggerConfig)
	trigger["id"] = triggerID

	// Preserve the loop's original settings verbatim so nothing is lost in
	// the copy (faithful as data permits). CEL/repeat/etc. are absent for a
	// one-node compatibility workflow.
	loopCompat := map[string]any{
		"loopId":         loopID,
		"actionType":     actionType,
		"actionTemplate": actionTemplate,
		"triggerType":    triggerType,
		"state":          loopState,
	}
	if triggerConfig != "" && triggerConfig != "{}" {
		loopCompat["triggerConfig"] = json.RawMessage(triggerConfig)
	}
	if stopConditions != "" && stopConditions != "{}" {
		loopCompat["stopConditions"] = json.RawMessage(stopConditions)
	}

	definition := map[string]any{
		"id":          workflowID,
		"name":        name,
		"version":     "1",
		"concurrency": 1,
		"directory":   directory,
		"triggers":    []any{trigger},
		"nodes": []any{map[string]any{
			"id":   nodeID,
			"name": name,
			"type": "agent",
		}},
		"dependencies": []any{},
		"loopCompat":   loopCompat,
	}
	definitionJSON, err := json.Marshal(definition)
	if err != nil {
		return fmt.Errorf("encoding definition: %w", err)
	}

	if _, err := tx.Exec(`INSERT INTO workflow_definition (id, name, current_revision, created_at, updated_at) VALUES (?, ?, 1, ?, ?)`,
		workflowID, name, createdAt, updatedAt); err != nil {
		return fmt.Errorf("inserting workflow definition: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO workflow_version (id, workflow_id, name, revision, metadata_version, definition_json, concurrency, created_at) VALUES (?, ?, ?, 1, '1', ?, 1, ?)`,
		versionID, workflowID, name, string(definitionJSON), createdAt); err != nil {
		return fmt.Errorf("inserting workflow version: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO workflow_version_node (version_id, node_id, name, type, position) VALUES (?, ?, ?, 'agent', 0)`,
		versionID, nodeID, name); err != nil {
		return fmt.Errorf("inserting workflow node: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO loop_workflow_map (loop_id, workflow_id, version_id, trigger_id, migrated_at) VALUES (?, ?, ?, ?, ?)`,
		loopID, workflowID, versionID, triggerID, updatedAt); err != nil {
		return fmt.Errorf("inserting loop_workflow_map: %w", err)
	}

	return migrateLoopIterations(tx, loopID, workflowID, versionID, nodeID, triggerID, name)
}

// migrateLoopIterations turns each historical loop iteration into one
// workflow run (one firing == one run in the trigger model) with a single
// node run + node attempt reflecting the iteration's outcome.
func migrateLoopIterations(tx *sql.Tx, loopID, workflowID, versionID, nodeID, triggerID, name string) error {
	rows, err := tx.Query(`
		SELECT seq, fired_at, started_at, completed_at, trigger_detail,
		       rendered_prompt, target_session_id, child_session_id, outcome, summary
		FROM loop_iterations WHERE loop_id = ? ORDER BY seq`, loopID)
	if err != nil {
		return fmt.Errorf("listing loop iterations: %w", err)
	}
	type iterRow struct {
		seq                             int
		firedAt, startedAt, completedAt int64
		detail, prompt, target, child   string
		outcome, summary                string
	}
	var iters []iterRow
	for rows.Next() {
		var it iterRow
		if err := rows.Scan(&it.seq, &it.firedAt, &it.startedAt, &it.completedAt,
			&it.detail, &it.prompt, &it.target, &it.child, &it.outcome, &it.summary); err != nil {
			rows.Close()
			return fmt.Errorf("scanning iteration: %w", err)
		}
		iters = append(iters, it)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("reading iterations: %w", err)
	}

	for _, it := range iters {
		runState, nodeState, attemptState := iterationStates(it.outcome)
		runID := fmt.Sprintf("wfr_loop_%s_%d", loopID, it.seq)
		session := it.child
		if session == "" {
			session = it.target
		}
		snapshot, err := json.Marshal(map[string]any{
			"id": triggerID, "type": "interval", "overlap": "skip",
			"versionId": versionID, "firedAt": it.firedAt, "detail": it.detail,
		})
		if err != nil {
			return fmt.Errorf("encoding run snapshot: %w", err)
		}
		completed := interface{}(nil)
		if it.completedAt != 0 {
			completed = it.completedAt
		}
		if _, err := tx.Exec(`INSERT INTO workflow_run (id, workflow_id, version_id, state, created_at, updated_at, completed_at, trigger_snapshot_json) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			runID, workflowID, versionID, runState, it.firedAt, it.completedAt, completed, string(snapshot)); err != nil {
			return fmt.Errorf("inserting workflow run: %w", err)
		}
		if _, err := tx.Exec(`INSERT INTO workflow_node_run (run_id, node_id, state, position, ready_at, completed_at) VALUES (?, ?, ?, 0, ?, ?)`,
			runID, nodeID, nodeState, it.firedAt, completed); err != nil {
			return fmt.Errorf("inserting workflow node run: %w", err)
		}
		errText := ""
		if it.outcome == "error" {
			errText = it.summary
		}
		if _, err := tx.Exec(`INSERT INTO workflow_node_attempt (run_id, node_id, seq, state, started_at, completed_at, error, outputs_json, platform, session_id, session_state, affinity, directory) VALUES (?, ?, 1, ?, ?, ?, ?, '{}', 'opencode', ?, '', '', '')`,
			runID, nodeID, attemptState, it.startedAt, completed, errText, session); err != nil {
			return fmt.Errorf("inserting workflow node attempt: %w", err)
		}
	}
	return nil
}

// iterationStates maps a loop iteration outcome to (runState, nodeState,
// attemptState) in the workflow vocabulary. A pending/unknown iteration was
// in flight when its data was captured; it is recorded as a terminal
// canceled historical run rather than a live "active" run, so the migration
// never resurrects a run the new scheduler would try to dispatch.
func iterationStates(outcome string) (string, string, string) {
	switch outcome {
	case "ok":
		return "successful", "successful", "successful"
	case "error":
		return "failed", "failed", "failed"
	default: // skipped / pending / unknown
		return "canceled", "canceled", "canceled"
	}
}

// loopTriggerToWorkflow maps a loop trigger_type + config to the workflow
// trigger vocabulary (see internal/workflows trigger constants).
func loopTriggerToWorkflow(triggerType, triggerConfig string) map[string]any {
	var cfg struct {
		IntervalSeconds int    `json:"interval_seconds"`
		CronExpr        string `json:"cron_expr"`
		PRNumber        int    `json:"pr_number"`
		PollSeconds     int    `json:"poll_seconds"`
		TargetSessionID string `json:"target_session_id"`
	}
	if triggerConfig != "" {
		_ = json.Unmarshal([]byte(triggerConfig), &cfg)
	}
	trigger := map[string]any{"overlap": "skip"}
	switch triggerType {
	case "schedule":
		trigger["type"] = "interval"
		trigger["intervalSeconds"] = cfg.IntervalSeconds
	case "cron":
		trigger["type"] = "cron"
		trigger["cron"] = cfg.CronExpr
	case "pr_event":
		trigger["type"] = "pr"
		trigger["prNumber"] = cfg.PRNumber
		if cfg.PollSeconds > 0 {
			trigger["pollSeconds"] = cfg.PollSeconds
		}
	case "turn_complete":
		trigger["type"] = "turn_completion"
	case "child_complete":
		trigger["type"] = "child_completion"
	default:
		trigger["type"] = "manual"
	}
	return trigger
}
