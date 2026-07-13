package state

import (
	"strings"
	"testing"
)

func TestResolveWorkflowAttempt(t *testing.T) {
	db := openTestStateDB(t)
	version, err := db.InsertWorkflowVersion(WorkflowVersion{
		ID: "version-1", WorkflowID: "workflow-1", Name: "Workflow", MetadataVersion: "1",
		DefinitionJSON: `{"id":"workflow-1","name":"Workflow","version":"1","concurrency":1,"nodes":[{"id":"approval","name":"Approval","type":"approval"}],"dependencies":[]}`,
		Concurrency:    1, CreatedAt: 1, Nodes: []WorkflowNode{{ID: "approval", Name: "Approval", Type: "approval"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.InsertWorkflowRun(WorkflowRun{ID: "run-1", WorkflowID: version.WorkflowID, VersionID: version.ID, State: "active", CreatedAt: 1, UpdatedAt: 1, Nodes: []WorkflowNodeRun{{NodeID: "approval", State: "ready"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec(`UPDATE workflow_run SET state = 'paused' WHERE id = 'run-1'; UPDATE workflow_node_attempt SET state = 'unknown' WHERE run_id = 'run-1'`); err != nil {
		t.Fatal(err)
	}
	run, err := db.GetWorkflowRun("run-1")
	if err != nil {
		t.Fatal(err)
	}
	attemptID := run.Nodes[0].Attempts[0].ID
	if err := db.ResolveWorkflowAttempt("run-1", attemptID, "successful", 2); err != nil {
		t.Fatal(err)
	}
	run, err = db.GetWorkflowRun("run-1")
	if err != nil {
		t.Fatal(err)
	}
	if run.State != "successful" || run.Nodes[0].State != "successful" || run.Nodes[0].Attempts[0].State != "successful" {
		t.Fatalf("unknown attempt was not resolved atomically: %+v", run)
	}
	if err := db.ResolveWorkflowAttempt("run-1", attemptID, "failed", 3); err == nil || !strings.Contains(err.Error(), "unknown attempt") {
		t.Fatalf("expected actionable already-resolved error, got %v", err)
	}
}
