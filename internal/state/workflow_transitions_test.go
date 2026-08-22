package state

import (
	"strings"
	"testing"
)

// seedTransitionRun inserts a two-node workflow (gate -> ship) whose first
// node is ready and second pending, and returns the run's version ID.
func seedTransitionRun(t *testing.T, db *DB, runID, gateType, shipType string) string {
	t.Helper()
	version, err := db.InsertWorkflowVersion(t.Context(), WorkflowVersion{
		ID: "version-" + runID, WorkflowID: "workflow-" + runID, Name: "Workflow",
		MetadataVersion: "1", DefinitionJSON: `{}`, Concurrency: 1, CreatedAt: 1,
		Nodes: []WorkflowNode{
			{ID: "gate", Name: "Gate", Type: gateType, Position: 0},
			{ID: "ship", Name: "Ship", Type: shipType, Position: 1},
		},
		Dependencies: []WorkflowDependency{{From: "gate", To: "ship"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.InsertWorkflowRun(t.Context(), WorkflowRun{
		ID: runID, WorkflowID: version.WorkflowID, VersionID: version.ID,
		State: "active", CreatedAt: 1, UpdatedAt: 1,
		Nodes: []WorkflowNodeRun{
			{NodeID: "gate", State: "ready", Position: 0, ReadyAt: 1},
			{NodeID: "ship", State: "pending", Position: 1},
		},
	}); err != nil {
		t.Fatal(err)
	}
	return version.ID
}

// nodeRun returns the named node of a run, failing if it's absent.
func nodeRun(t *testing.T, db *DB, runID, nodeID string) WorkflowNodeRun {
	t.Helper()
	run, err := db.GetWorkflowRun(t.Context(), runID)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range run.Nodes {
		if n.NodeID == nodeID {
			return n
		}
	}
	t.Fatalf("node %q not found in run %s", nodeID, runID)
	return WorkflowNodeRun{}
}

// TestApproveWorkflowNodeReadiesDownstreamThenCompletes walks the whole
// approval chain, asserting the node_run / node_attempt rows at each step.
func TestApproveWorkflowNodeReadiesDownstreamThenCompletes(t *testing.T) {
	db := openTestStateDB(t)
	seedTransitionRun(t, db, "run-1", "approval", "approval")

	if err := db.ApproveWorkflowNode(t.Context(), "run-1", "gate", 10); err != nil {
		t.Fatalf("approving gate: %v", err)
	}

	gate := nodeRun(t, db, "run-1", "gate")
	if gate.State != "successful" || gate.CompletedAt != 10 {
		t.Errorf("gate = %+v, want successful at 10", gate)
	}
	if len(gate.Attempts) != 1 || gate.Attempts[0].State != "successful" {
		t.Errorf("gate attempts = %+v, want one successful", gate.Attempts)
	}

	// The dependency is satisfied, so ship must be readied and given a
	// waiting attempt to hang the approval off.
	ship := nodeRun(t, db, "run-1", "ship")
	if ship.State != "ready" || ship.ReadyAt != 10 {
		t.Errorf("ship = %+v, want ready at 10", ship)
	}
	if len(ship.Attempts) != 1 || ship.Attempts[0].State != "waiting" {
		t.Errorf("ship attempts = %+v, want one waiting", ship.Attempts)
	}

	run, err := db.GetWorkflowRun(t.Context(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if run.State != "active" {
		t.Errorf("run state = %q, want active while ship is outstanding", run.State)
	}

	// Approving the last node settles the run.
	if err := db.ApproveWorkflowNode(t.Context(), "run-1", "ship", 20); err != nil {
		t.Fatalf("approving ship: %v", err)
	}
	run, err = db.GetWorkflowRun(t.Context(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if run.State != "successful" || run.CompletedAt != 20 {
		t.Errorf("run = %+v, want successful at 20", run)
	}
}

func TestApproveWorkflowNodeRejectsBadTransitions(t *testing.T) {
	tests := []struct {
		name    string
		nodeID  string
		setup   func(t *testing.T, db *DB)
		wantErr string
	}{
		{
			name:   "pending node is not ready",
			nodeID: "ship",
			// ship stays pending behind its dependency on gate.
			wantErr: "not ready",
		},
		{
			name:    "unknown node",
			nodeID:  "nope",
			wantErr: "getting workflow node",
		},
		{
			name:   "paused run is not active",
			nodeID: "gate",
			setup: func(t *testing.T, db *DB) {
				t.Helper()
				if _, err := db.db.Exec(`UPDATE workflow_run SET state = 'paused' WHERE id = 'run-1'`); err != nil {
					t.Fatal(err)
				}
			},
			wantErr: "not active",
		},
		{
			name:   "non-approval node cannot be approved",
			nodeID: "gate",
			setup: func(t *testing.T, db *DB) {
				t.Helper()
				if _, err := db.db.Exec(`UPDATE workflow_version_node SET type = 'command' WHERE node_id = 'gate'`); err != nil {
					t.Fatal(err)
				}
			},
			wantErr: "is not an approval",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := openTestStateDB(t)
			seedTransitionRun(t, db, "run-1", "approval", "approval")
			if tc.setup != nil {
				tc.setup(t, db)
			}
			err := db.ApproveWorkflowNode(t.Context(), "run-1", tc.nodeID, 10)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want one containing %q", err, tc.wantErr)
			}
			// A rejected approval must leave the gate untouched.
			if gate := nodeRun(t, db, "run-1", "gate"); gate.State == "successful" {
				t.Errorf("gate was completed despite the rejected approval: %+v", gate)
			}
		})
	}
}

func TestApproveWorkflowNodeUnknownRun(t *testing.T) {
	db := openTestStateDB(t)
	err := db.ApproveWorkflowNode(t.Context(), "nope", "gate", 10)
	if err == nil || !strings.Contains(err.Error(), "getting workflow run") {
		t.Fatalf("error = %v, want a missing-run error", err)
	}
}

// startGateAttempt moves the seeded gate attempt into 'running' so the
// recovery transitions have something interrupted to act on.
func startGateAttempt(t *testing.T, db *DB) int64 {
	t.Helper()
	if _, err := db.db.Exec(`UPDATE workflow_node_attempt SET state = 'running' WHERE run_id = 'run-1' AND node_id = 'gate'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec(`UPDATE workflow_node_run SET state = 'running' WHERE run_id = 'run-1' AND node_id = 'gate'`); err != nil {
		t.Fatal(err)
	}
	return nodeRun(t, db, "run-1", "gate").Attempts[0].ID
}

// TestMarkWorkflowAttemptUnknownPausesRun covers the uncertain-side-effects
// path: the attempt and node go unknown and the run pauses so nothing
// downstream is scheduled until a human decides.
func TestMarkWorkflowAttemptUnknownPausesRun(t *testing.T) {
	db := openTestStateDB(t)
	seedTransitionRun(t, db, "run-1", "command", "command")
	attemptID := startGateAttempt(t, db)

	if err := db.MarkWorkflowAttemptUnknown(t.Context(), "run-1", "gate", attemptID, "restarted mid-flight", 30); err != nil {
		t.Fatalf("MarkWorkflowAttemptUnknown: %v", err)
	}

	gate := nodeRun(t, db, "run-1", "gate")
	if gate.State != "unknown" {
		t.Errorf("gate state = %q, want unknown", gate.State)
	}
	if len(gate.Attempts) != 1 {
		t.Fatalf("gate attempts = %+v, want one", gate.Attempts)
	}
	attempt := gate.Attempts[0]
	if attempt.State != "unknown" || attempt.Error != "restarted mid-flight" || attempt.CompletedAt != 30 {
		t.Errorf("attempt = %+v, want unknown with the reason recorded at 30", attempt)
	}

	run, err := db.GetWorkflowRun(t.Context(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if run.State != "paused" {
		t.Errorf("run state = %q, want paused", run.State)
	}
	// Downstream must not have been readied.
	if ship := nodeRun(t, db, "run-1", "ship"); ship.State != "pending" {
		t.Errorf("ship state = %q, want pending", ship.State)
	}
}

// TestRetryWorkflowAttemptCreatesFreshAttempt covers the retry-safe path:
// the interrupted attempt is failed with an audit trail and a new waiting
// attempt replaces it, with the run left active for the next tick.
func TestRetryWorkflowAttemptCreatesFreshAttempt(t *testing.T) {
	db := openTestStateDB(t)
	seedTransitionRun(t, db, "run-1", "command", "command")
	attemptID := startGateAttempt(t, db)

	if err := db.RetryWorkflowAttempt(t.Context(), "run-1", "gate", attemptID, "launch interrupted", "operator", 40); err != nil {
		t.Fatalf("RetryWorkflowAttempt: %v", err)
	}

	gate := nodeRun(t, db, "run-1", "gate")
	if gate.State != "ready" || gate.ReadyAt != 40 || gate.CompletedAt != 0 {
		t.Errorf("gate = %+v, want ready at 40 with no completion", gate)
	}
	if len(gate.Attempts) != 2 {
		t.Fatalf("gate attempts = %+v, want the failed one plus a retry", gate.Attempts)
	}
	failed, retry := gate.Attempts[0], gate.Attempts[1]
	if failed.ID != attemptID || failed.State != "failed" || failed.Error != "launch interrupted" {
		t.Errorf("original attempt = %+v, want failed with the reason", failed)
	}
	if failed.ResolvedBy != "operator" || failed.ResolvedAt != 40 {
		t.Errorf("original attempt audit = %+v, want resolved by operator at 40", failed)
	}
	if retry.State != "waiting" || retry.Seq != failed.Seq+1 {
		t.Errorf("retry attempt = %+v, want waiting at seq %d", retry, failed.Seq+1)
	}

	run, err := db.GetWorkflowRun(t.Context(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if run.State != "active" || run.UpdatedAt != 40 {
		t.Errorf("run = %+v, want still active and touched at 40", run)
	}
}
