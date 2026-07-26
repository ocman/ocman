package state

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestArchiveWorkflowVersionHidesDefinitionButKeepsRuns(t *testing.T) {
	db := openTestStateDB(t)
	version, err := db.InsertWorkflowVersion(WorkflowVersion{ID: "version-1", WorkflowID: "workflow-1", Name: "Workflow", MetadataVersion: "1", DefinitionJSON: `{}`, Concurrency: 1, CreatedAt: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.InsertWorkflowRun(WorkflowRun{ID: "run-1", WorkflowID: version.WorkflowID, VersionID: version.ID, State: "successful", CreatedAt: 1, UpdatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	if err := db.ArchiveWorkflowVersion(version.ID, 2); err != nil {
		t.Fatal(err)
	}
	versions, err := db.ListWorkflowVersions()
	if err != nil || len(versions) != 0 {
		t.Fatalf("archived workflow still listed: %#v, %v", versions, err)
	}
	runs, err := db.ListWorkflowRuns()
	if err != nil || len(runs) != 1 {
		t.Fatalf("archiving removed run history: %#v, %v", runs, err)
	}
	if _, err := db.GetActiveWorkflowVersion(version.WorkflowID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("archived workflow remained active: %v", err)
	}
}

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

// TestMarkWorkflowAttemptUnknownIgnoresSettledAttempt pins that an
// attempt which settled between the caller's snapshot and its write is
// left alone. recoverInterrupted reads a GetRun snapshot without
// dispatchMu and then does network I/O before writing, so the attempt
// can settle in between. Only the attempt update was state-guarded, so
// it matched zero rows while the node still flipped to unknown and the
// run still paused — leaving a node ResolveWorkflowAttemptBy rejects
// ("unknown attempt not found") on a paused run that RetryFrom also
// refuses. Only cancelling could clear it.
func TestMarkWorkflowAttemptUnknownIgnoresSettledAttempt(t *testing.T) {
	db := openTestStateDB(t)
	if _, err := db.InsertWorkflowVersion(WorkflowVersion{
		ID: "version-1", WorkflowID: "workflow-1", Name: "Workflow", MetadataVersion: "1",
		DefinitionJSON: `{"id":"workflow-1","name":"Workflow","version":"1","concurrency":1,"nodes":[{"id":"review","name":"Review","type":"agent"}],"dependencies":[]}`,
		Concurrency:    1, CreatedAt: 1, Nodes: []WorkflowNode{{ID: "review", Name: "Review", Type: "agent"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.InsertWorkflowRun(WorkflowRun{
		ID: "run-1", WorkflowID: "workflow-1", VersionID: "version-1", State: "active",
		CreatedAt: 1, UpdatedAt: 1,
		Nodes:     []WorkflowNodeRun{{NodeID: "review", Type: "agent", State: "ready"}},
	}); err != nil {
		t.Fatal(err)
	}
	run, err := db.GetWorkflowRun("run-1")
	if err != nil {
		t.Fatal(err)
	}
	attemptID := run.Nodes[0].Attempts[0].ID
	if _, err := db.db.Exec(`UPDATE workflow_node_attempt SET state = 'running' WHERE id = ?;
		UPDATE workflow_node_run SET state = 'running' WHERE run_id = 'run-1' AND node_id = 'review'`, attemptID); err != nil {
		t.Fatal(err)
	}

	// The attempt settles successfully while the caller is mid-probe.
	if err := db.CompleteWorkflowAgentNode("run-1", "review", attemptID,
		WorkflowAgentResult{Successful: true, SessionState: "done", OutputsJSON: "{}"}, 2); err != nil {
		t.Fatal(err)
	}
	if settled, err := db.GetWorkflowRun("run-1"); err != nil {
		t.Fatal(err)
	} else if settled.Nodes[0].Attempts[0].State != "successful" {
		t.Fatalf("precondition: attempt = %+v, want settled successful", settled.Nodes[0].Attempts[0])
	}

	if err := db.MarkWorkflowAttemptUnknown("run-1", "review", attemptID, "unreachable", 3); err != nil {
		t.Fatal(err)
	}

	after, err := db.GetWorkflowRun("run-1")
	if err != nil {
		t.Fatal(err)
	}
	if after.Nodes[0].State == "unknown" {
		t.Errorf("node flipped to unknown despite the attempt already settling")
	}
	if after.State == "paused" {
		t.Errorf("run paused despite the attempt already settling")
	}
	if got := after.Nodes[0].Attempts[0].State; got != "successful" {
		t.Errorf("attempt state = %q, want it left settled", got)
	}
}

func TestWorkflowLifecyclePersistence(t *testing.T) {
	db := openTestStateDB(t)
	definition := WorkflowVersion{
		WorkflowID: "workflow-1", Name: "Workflow", MetadataVersion: "1", DefinitionJSON: `{}`,
		Concurrency: 2, RetentionDays: 7,
		Nodes: []WorkflowNode{
			{ID: "build", Name: "Build", Type: "command", Position: 0},
			{ID: "review", Name: "Review", Type: "agent", Position: 1},
		},
		Dependencies: []WorkflowDependency{{From: "build", To: "review"}},
	}
	definition.ID, definition.CreatedAt = "version-1", 1
	v1, err := db.InsertWorkflowVersion(definition)
	if err != nil || !v1.Active || v1.Revision != 1 {
		t.Fatalf("insert first version: %+v, %v", v1, err)
	}
	definition.ID, definition.CreatedAt = "version-2", 2
	v2, err := db.InsertWorkflowVersion(definition)
	if err != nil || v2.Active || v2.Revision != 2 {
		t.Fatalf("insert second version: %+v, %v", v2, err)
	}
	activeV2, err := db.ActivateWorkflowVersion(v2.ID, 3)
	if err != nil || !activeV2.Active || len(activeV2.Nodes) != 2 || len(activeV2.Dependencies) != 1 {
		t.Fatalf("activate second version: %+v, %v", activeV2, err)
	}
	versions, err := db.ListWorkflowVersions()
	if err != nil || len(versions) != 2 {
		t.Fatalf("list versions: %+v, %v", versions, err)
	}
	if _, err := db.DeactivateWorkflowVersion(v2.ID, 4); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ActivateWorkflowVersion(v2.ID, 5); err != nil {
		t.Fatal(err)
	}
	active, err := db.GetActiveWorkflowVersion(v2.WorkflowID)
	if err != nil || active.ID != v2.ID {
		t.Fatalf("active version: %+v, %v", active, err)
	}

	run := WorkflowRun{
		ID: "run-1", WorkflowID: v2.WorkflowID, VersionID: v2.ID, State: "active",
		CreatedAt: 10, UpdatedAt: 10, TriggerSnapshotJSON: `{"type":"manual"}`,
		Nodes: []WorkflowNodeRun{
			{NodeID: "build", State: "ready", Position: 0, ReadyAt: 10},
			{NodeID: "review", State: "pending", Position: 1},
		},
	}
	if err := db.InsertWorkflowRun(run); err != nil {
		t.Fatal(err)
	}
	if started, err := db.StartWorkflowCommand(run.ID, "build", []WorkflowResourceRequest{{Pool: "", Units: 1, Capacity: 2}}, nil, 11); err != nil || !started {
		t.Fatalf("start command: %v, %v", started, err)
	}
	if err := db.CompleteWorkflowCommand(run.ID, "build", WorkflowCommandResult{State: "successful", ExitCode: 0, Stdout: `{"built":true}`, OutputsJSON: `{"built":true}`}, 12); err != nil {
		t.Fatal(err)
	}
	persisted, err := db.GetWorkflowRun(run.ID)
	if err != nil || persisted.State != "active" || persisted.Nodes[1].State != "ready" {
		t.Fatalf("command completion: %+v, %v", persisted, err)
	}
	attemptID := persisted.Nodes[1].Attempts[0].ID
	if claimed, err := db.ClaimWorkflowAgentAttempt(run.ID, "review", attemptID, "host", "/repo", []WorkflowResourceRequest{{Pool: "", Units: 1, Capacity: 2}}, nil, 13); err != nil || !claimed {
		t.Fatalf("claim agent: %v, %v", claimed, err)
	}
	if err := db.AttachWorkflowAgentSession(run.ID, "review", attemptID, "opencode", "session-1", "busy", 14); err != nil {
		t.Fatal(err)
	}
	if err := db.SetWorkflowAgentSessionState(run.ID, "review", attemptID, "idle", "", 15); err != nil {
		t.Fatal(err)
	}
	if err := db.CompleteWorkflowAgentNode(run.ID, "review", attemptID, WorkflowAgentResult{Successful: true, SessionState: "idle", Output: `{"approved":true}`, OutputsJSON: `{"approved":true}`}, 16); err != nil {
		t.Fatal(err)
	}
	persisted, err = db.GetWorkflowRun(run.ID)
	if err != nil || persisted.State != "successful" || persisted.CompletedAt != 16 || persisted.Nodes[1].Attempts[0].OutputsJSON != `{"approved":true}` {
		t.Fatalf("agent completion: %+v, %v", persisted, err)
	}
	runs, err := db.ListWorkflowRuns()
	if err != nil || len(runs) != 1 || runs[0].TriggerSnapshotJSON != run.TriggerSnapshotJSON {
		t.Fatalf("list runs: %+v, %v", runs, err)
	}

	child := WorkflowRun{ID: "run-child", WorkflowID: v2.WorkflowID, VersionID: v2.ID, State: "successful", CreatedAt: 20, UpdatedAt: 20, ParentRunID: run.ID, ParentNodeID: "review", ItemKey: "item", ItemIndex: 1}
	if err := db.InsertWorkflowRun(child); err != nil {
		t.Fatal(err)
	}
	children, err := db.ListWorkflowChildRuns(run.ID)
	if err != nil || len(children) != 1 || children[0].ItemKey != "item" || children[0].ItemIndex != 1 {
		t.Fatalf("list child runs: %+v, %v", children, err)
	}

	for _, id := range []string{"run-cancel", "run-fail"} {
		candidate := run
		candidate.ID, candidate.CreatedAt, candidate.UpdatedAt = id, 30, 30
		if err := db.InsertWorkflowRun(candidate); err != nil {
			t.Fatal(err)
		}
		if started, err := db.StartWorkflowCommand(id, "build", []WorkflowResourceRequest{{Pool: "", Units: 1, Capacity: 2}}, nil, 31); err != nil || !started {
			t.Fatalf("start %s: %v, %v", id, started, err)
		}
	}
	if err := db.SetWorkflowRunState("run-cancel", "active", "paused", 32); err != nil {
		t.Fatal(err)
	}
	if err := db.SetWorkflowRunState("run-cancel", "paused", "active", 33); err != nil {
		t.Fatal(err)
	}
	if err := db.SetWorkflowRunState("run-cancel", "active", "canceled", 34); err != nil {
		t.Fatal(err)
	}
	if err := db.FailWorkflowRun("run-fail", "budget exhausted", 35); err != nil {
		t.Fatal(err)
	}
	for _, want := range []struct{ id, state string }{{"run-cancel", "canceled"}, {"run-fail", "failed"}} {
		got, err := db.GetWorkflowRun(want.id)
		if err != nil || got.State != want.state || got.CompletedAt == 0 {
			t.Fatalf("settled run %s: %+v, %v", want.id, got, err)
		}
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
	started, err := db.StartWorkflowCommand("run-1", "one", req, nil, 10)
	if err != nil || !started {
		t.Fatalf("first acquire: started=%v err=%v", started, err)
	}
	// Node "two" also wants the "gpu" pool (has room) AND "compiler" (full).
	// It must acquire NOTHING and not start.
	req2 := []WorkflowResourceRequest{{Pool: "", Units: 1, Capacity: 2}, {Pool: "gpu", Units: 1, Capacity: 4}, {Pool: "compiler", Units: 1, Capacity: 1}}
	started, err = db.StartWorkflowCommand("run-1", "two", req2, nil, 11)
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
	if started, err := db.StartWorkflowCommand("r", "one", []WorkflowResourceRequest{{Pool: "", Units: 1, Capacity: 1}, {Pool: "compiler", Units: 1, Capacity: 1}}, nil, 5); err != nil || !started {
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
