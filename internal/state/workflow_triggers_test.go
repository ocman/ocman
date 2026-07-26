package state

import "testing"

func TestWorkflowTriggerQueuePersistsAndStartsAtomically(t *testing.T) {
	db := openTestStateDB(t)
	version, err := db.InsertWorkflowVersion(WorkflowVersion{
		ID: "version-1", WorkflowID: "workflow-1", Name: "Workflow", MetadataVersion: "1", DefinitionJSON: `{}`,
		Concurrency: 1, CreatedAt: 1, Nodes: []WorkflowNode{{ID: "approve", Name: "Approve", Type: "approval"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	running := true
	triggerState := WorkflowTriggerState{VersionID: version.ID, TriggerID: "cron", DetectionJSON: `{"slot":1}`, LastDecision: "queued", LastCheckedAt: 2, NextCheckAt: 3, LastRunning: &running}
	firing := WorkflowTriggerFiring{VersionID: version.ID, TriggerID: "cron", FiredAt: 2, Detail: "slot", SnapshotJSON: `{"id":"cron"}`, Decision: "queued"}
	if err := db.CommitWorkflowTriggerFiring(nil, firing, triggerState); err != nil {
		t.Fatal(err)
	}
	current, err := db.ListCurrentWorkflowVersions()
	if err != nil || len(current) != 1 || current[0].ID != version.ID {
		t.Fatalf("current versions: %+v, %v", current, err)
	}
	queuedVersions, err := db.ListQueuedWorkflowVersions()
	if err != nil || len(queuedVersions) != 1 || queuedVersions[0].ID != version.ID {
		t.Fatalf("queued versions: %+v, %v", queuedVersions, err)
	}
	storedState, err := db.GetWorkflowTriggerState(version.ID, "cron")
	if err != nil || storedState.LastRunning == nil || !*storedState.LastRunning || storedState.NextCheckAt != 3 {
		t.Fatalf("trigger state: %+v, %v", storedState, err)
	}
	queued, err := db.NextQueuedWorkflowTriggerFiring(version.ID, "cron")
	if err != nil || queued == nil {
		t.Fatalf("queued firing: %+v, %v", queued, err)
	}
	if count, err := db.CountQueuedWorkflowTriggerFirings(version.ID, "cron"); err != nil || count != 1 {
		t.Fatalf("queued count: %d, %v", count, err)
	}

	run := WorkflowRun{ID: "run-1", WorkflowID: version.WorkflowID, VersionID: version.ID, State: "active", CreatedAt: 4, UpdatedAt: 4, TriggerSnapshotJSON: firing.SnapshotJSON, Nodes: []WorkflowNodeRun{{NodeID: "approve", State: "ready"}}}
	triggerState.LastDecision, triggerState.LastRunID = "started", run.ID
	if err := db.InsertWorkflowRunFromQueued(run, queued.ID, 4, triggerState); err != nil {
		t.Fatal(err)
	}
	if count, err := db.CountActiveWorkflowTriggerRuns(version.ID, "cron"); err != nil || count != 1 {
		t.Fatalf("active count: %d, %v", count, err)
	}
	if id, err := db.ActiveWorkflowTriggerRunID(version.ID, "cron"); err != nil || id != run.ID {
		t.Fatalf("active run: %q, %v", id, err)
	}

	duplicate := run
	duplicate.ID = "rolled-back-run"
	if err := db.InsertWorkflowRunFromQueued(duplicate, queued.ID, 5, triggerState); err == nil {
		t.Fatal("started the same queued firing twice")
	}
	if _, err := db.GetWorkflowRun(duplicate.ID); err == nil {
		t.Fatal("queued firing race retained an orphan run")
	}
}

// Archiving a workflow must stop it scheduling. The trigger scheduler
// iterates ListCurrentWorkflowVersions / ListQueuedWorkflowVersions, so
// both have to honour workflow_definition.archived_at the same way the
// user-facing ListWorkflowVersions does.
func TestArchivedWorkflowVersionsStopScheduling(t *testing.T) {
	db := openTestStateDB(t)
	version, err := db.InsertWorkflowVersion(WorkflowVersion{
		ID: "version-1", WorkflowID: "workflow-1", Name: "Workflow", MetadataVersion: "1", DefinitionJSON: `{}`,
		Concurrency: 1, CreatedAt: 1, Nodes: []WorkflowNode{{ID: "approve", Name: "Approve", Type: "approval"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	firing := WorkflowTriggerFiring{VersionID: version.ID, TriggerID: "cron", FiredAt: 2, Detail: "slot", SnapshotJSON: `{"id":"cron"}`, Decision: "queued"}
	triggerState := WorkflowTriggerState{VersionID: version.ID, TriggerID: "cron", DetectionJSON: `{"slot":1}`, LastDecision: "queued", LastCheckedAt: 2, NextCheckAt: 3}
	if err := db.CommitWorkflowTriggerFiring(nil, firing, triggerState); err != nil {
		t.Fatal(err)
	}

	// Sanity: schedulable before archiving.
	if current, err := db.ListCurrentWorkflowVersions(); err != nil || len(current) != 1 {
		t.Fatalf("pre-archive current versions: %+v, %v", current, err)
	}
	if queued, err := db.ListQueuedWorkflowVersions(); err != nil || len(queued) != 1 {
		t.Fatalf("pre-archive queued versions: %+v, %v", queued, err)
	}

	if err := db.ArchiveWorkflowVersion(version.ID, 5); err != nil {
		t.Fatal(err)
	}

	if current, err := db.ListCurrentWorkflowVersions(); err != nil || len(current) != 0 {
		t.Fatalf("archived workflow still scheduled by ListCurrentWorkflowVersions: %+v, %v", current, err)
	}
	if queued, err := db.ListQueuedWorkflowVersions(); err != nil || len(queued) != 0 {
		t.Fatalf("archived workflow still scheduled by ListQueuedWorkflowVersions: %+v, %v", queued, err)
	}
}

func TestWorkflowTriggerFiringRollsBackRunOnFailure(t *testing.T) {
	db := openTestStateDB(t)
	version, err := db.InsertWorkflowVersion(WorkflowVersion{ID: "version-1", WorkflowID: "workflow-1", Name: "Workflow", MetadataVersion: "1", DefinitionJSON: `{}`, Concurrency: 1, CreatedAt: 1})
	if err != nil {
		t.Fatal(err)
	}
	run := WorkflowRun{ID: "rolled-back-run", WorkflowID: version.WorkflowID, VersionID: version.ID, State: "active", CreatedAt: 2, UpdatedAt: 2}
	firing := WorkflowTriggerFiring{VersionID: version.ID, TriggerID: "fail", FiredAt: 2, Detail: "slot", SnapshotJSON: `{}`, Decision: "started"}
	if _, err := db.db.Exec(`CREATE TRIGGER fail_trigger_firing BEFORE INSERT ON workflow_trigger_firing WHEN NEW.trigger_id = 'fail' BEGIN SELECT RAISE(ABORT, 'forced firing failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := db.CommitWorkflowTriggerFiring(&run, firing, WorkflowTriggerState{VersionID: version.ID, TriggerID: "cron"}); err == nil {
		t.Fatal("invalid firing committed")
	}
	if _, err := db.GetWorkflowRun(run.ID); err == nil {
		t.Fatal("failed firing transaction retained its run")
	}
}
