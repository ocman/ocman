package state

import (
	"path/filepath"
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

// seedResourceRun creates a two-command run with waiting attempts so the
// resource-acquisition store methods can be driven directly.
func seedResourceRun(t *testing.T) *DB {
	t.Helper()
	db := openTestStateDB(t)
	version, err := db.InsertWorkflowVersion(WorkflowVersion{
		ID: "version-1", WorkflowID: "workflow-1", Name: "Workflow", MetadataVersion: "1",
		DefinitionJSON: `{"id":"workflow-1","name":"Workflow","version":"1","concurrency":2}`,
		Concurrency:    2, CreatedAt: 1,
		Nodes: []WorkflowNode{{ID: "one", Name: "One", Type: "command", Position: 0}, {ID: "two", Name: "Two", Type: "command", Position: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.InsertWorkflowRun(WorkflowRun{ID: "run-1", WorkflowID: version.WorkflowID, VersionID: version.ID, State: "active", CreatedAt: 1, UpdatedAt: 1, Nodes: []WorkflowNodeRun{{NodeID: "one", State: "ready"}, {NodeID: "two", State: "ready"}}}); err != nil {
		t.Fatal(err)
	}
	return db
}

// TestStartWorkflowCommandAtomicAcquireAllOrNothing proves the scheduler
// never partially holds resources: if any requested pool lacks capacity,
// none are held and the attempt is not started.
func TestStartWorkflowCommandAtomicAcquireAllOrNothing(t *testing.T) {
	db := seedResourceRun(t)
	// Node "one" acquires the whole "compiler" pool (cap 1).
	req := []WorkflowResourceRequest{{Pool: "", Units: 1, Capacity: 2}, {Pool: "compiler", Units: 1, Capacity: 1}}
	started, err := db.StartWorkflowCommand("run-1", "one", req, 10)
	if err != nil || !started {
		t.Fatalf("first acquire: started=%v err=%v", started, err)
	}
	// Node "two" also wants the "gpu" pool (has room) AND "compiler" (full).
	// It must acquire NOTHING and not start.
	req2 := []WorkflowResourceRequest{{Pool: "", Units: 1, Capacity: 2}, {Pool: "gpu", Units: 1, Capacity: 4}, {Pool: "compiler", Units: 1, Capacity: 1}}
	started, err = db.StartWorkflowCommand("run-1", "two", req2, 11)
	if err != nil {
		t.Fatalf("second acquire err: %v", err)
	}
	if started {
		t.Fatal("second command started despite full compiler pool")
	}
	leases, err := db.ListWorkflowResourceLeases("run-1")
	if err != nil {
		t.Fatal(err)
	}
	// Only node "one" holds capacity ("" + compiler). No partial "gpu" lease.
	for _, lease := range leases {
		if lease.NodeID == "two" {
			t.Fatalf("failed acquire left a partial lease: %+v", lease)
		}
	}
	if len(leases) != 2 {
		t.Fatalf("expected exactly node one's two leases, got %+v", leases)
	}
}

// TestResourceLeasesSurviveReopen proves held capacity is durable so a
// restart can reconcile it (and the UI can show it).
func TestResourceLeasesSurviveReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	version, err := db.InsertWorkflowVersion(WorkflowVersion{
		ID: "v", WorkflowID: "w", Name: "W", MetadataVersion: "1",
		DefinitionJSON: `{"id":"w","name":"W","version":"1","concurrency":1}`,
		Concurrency:    1, CreatedAt: 1,
		Nodes: []WorkflowNode{{ID: "one", Name: "One", Type: "command"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.InsertWorkflowRun(WorkflowRun{ID: "r", WorkflowID: version.WorkflowID, VersionID: version.ID, State: "active", CreatedAt: 1, UpdatedAt: 1, Nodes: []WorkflowNodeRun{{NodeID: "one", State: "ready"}}}); err != nil {
		t.Fatal(err)
	}
	if started, err := db.StartWorkflowCommand("r", "one", []WorkflowResourceRequest{{Pool: "", Units: 1, Capacity: 1}, {Pool: "compiler", Units: 1, Capacity: 1}}, 5); err != nil || !started {
		t.Fatalf("acquire: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	leases, err := reopened.ListWorkflowResourceLeases("r")
	if err != nil {
		t.Fatal(err)
	}
	if len(leases) != 2 {
		t.Fatalf("held capacity not durable across reopen: %+v", leases)
	}
}
