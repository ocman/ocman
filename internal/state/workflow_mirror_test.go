package state

import "testing"

func mirrorFixture(t *testing.T) *DB {
	t.Helper()
	db := openTestStateDB(t)
	version, err := db.InsertWorkflowVersion(t.Context(), WorkflowVersion{ID: "version-1", WorkflowID: "workflow-1",
		Name: "Workflow", MetadataVersion: "1", DefinitionJSON: `{}`, Concurrency: 1, CreatedAt: 1,
		Nodes: []WorkflowNode{
			{ID: "build", Name: "Build", Type: "command", Position: 0},
			{ID: "ship", Name: "Ship", Type: "command", Position: 1},
		}})
	if err != nil {
		t.Fatal(err)
	}
	run := WorkflowRun{ID: "run-1", WorkflowID: version.WorkflowID, VersionID: version.ID,
		State: "active", CreatedAt: 1, UpdatedAt: 1,
		Nodes: []WorkflowNodeRun{
			{NodeID: "build", State: "pending", Position: 0},
			{NodeID: "ship", State: "pending", Position: 1},
		}}
	if err := db.InsertWorkflowRun(t.Context(), run); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestMirrorWorkflowRunProjectsStateAndOutput(t *testing.T) {
	db := mirrorFixture(t)
	changed, err := db.MirrorWorkflowRun(t.Context(), "run-1", WorkflowMirrorSnapshot{
		State:       "successful",
		CompletedAt: 90,
		Nodes: []WorkflowMirrorNode{
			{NodeID: "build", State: "successful", StartedAt: 10, CompletedAt: 20, Stdout: "built"},
			{NodeID: "ship", State: "failed", StartedAt: 20, CompletedAt: 30, Error: "boom", ExitCode: 2},
		},
	}, 100)
	if err != nil || !changed {
		t.Fatalf("Mirror() = %v, %v", changed, err)
	}
	stored, err := db.GetWorkflowRun(t.Context(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != "successful" || stored.CompletedAt != 90 {
		t.Fatalf("run = %+v", stored)
	}
	states := map[string]string{}
	for _, node := range stored.Nodes {
		states[node.NodeID] = node.State
		for _, attempt := range node.Attempts {
			if node.NodeID == "build" && attempt.Stdout != "built" {
				t.Errorf("build stdout = %q", attempt.Stdout)
			}
			if node.NodeID == "ship" && (attempt.Error != "boom" || attempt.ExitCode == nil || *attempt.ExitCode != 2) {
				t.Errorf("ship attempt = %+v", attempt)
			}
		}
	}
	if states["build"] != "successful" || states["ship"] != "failed" {
		t.Fatalf("node states = %v", states)
	}
}

// Polling is frequent and mostly idle. An unchanged snapshot must not
// report a change, or every subscriber wakes twice a second.
func TestMirrorWorkflowRunIsIdempotent(t *testing.T) {
	db := mirrorFixture(t)
	snapshot := WorkflowMirrorSnapshot{State: "active", Nodes: []WorkflowMirrorNode{
		{NodeID: "build", State: "running", StartedAt: 10},
	}}
	if changed, err := db.MirrorWorkflowRun(t.Context(), "run-1", snapshot, 100); err != nil || !changed {
		t.Fatalf("first Mirror() = %v, %v", changed, err)
	}
	changed, err := db.MirrorWorkflowRun(t.Context(), "run-1", snapshot, 200)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("unchanged snapshot reported a change")
	}
}

// A mapped child's internal steps have no ocman node row. They must be
// ignored rather than fail the whole snapshot.
func TestMirrorWorkflowRunIgnoresUnknownNodes(t *testing.T) {
	db := mirrorFixture(t)
	changed, err := db.MirrorWorkflowRun(t.Context(), "run-1", WorkflowMirrorSnapshot{
		State: "active",
		Nodes: []WorkflowMirrorNode{{NodeID: "not-a-node", State: "successful"}},
	}, 100)
	if err != nil || changed {
		t.Fatalf("Mirror() = %v, %v", changed, err)
	}
}

func TestMirrorWorkflowRunRejectsUnknownRun(t *testing.T) {
	db := mirrorFixture(t)
	if _, err := db.MirrorWorkflowRun(t.Context(), "ghost", WorkflowMirrorSnapshot{State: "active"}, 100); err == nil {
		t.Fatal("mirrored an unknown run")
	}
}

func TestListActiveExternalWorkflowRunsSkipsSettledAndUnlinked(t *testing.T) {
	db := mirrorFixture(t)
	// Not yet linked to an external execution.
	if runs, err := db.ListActiveExternalWorkflowRuns(t.Context()); err != nil || len(runs) != 0 {
		t.Fatalf("runs = %#v, %v", runs, err)
	}
	if err := db.SetWorkflowRunExternal(t.Context(), "run-1", "dagu-1", "dagu", 50); err != nil {
		t.Fatal(err)
	}
	runs, err := db.ListActiveExternalWorkflowRuns(t.Context())
	if err != nil || len(runs) != 1 || runs[0].ExternalID != "dagu-1" || runs[0].Runner != "dagu" {
		t.Fatalf("runs = %#v, %v", runs, err)
	}
	// A settled run's rows are final; a restart must not resurrect it.
	if _, err := db.MirrorWorkflowRun(t.Context(), "run-1", WorkflowMirrorSnapshot{State: "successful"}, 60); err != nil {
		t.Fatal(err)
	}
	if runs, err := db.ListActiveExternalWorkflowRuns(t.Context()); err != nil || len(runs) != 0 {
		t.Fatalf("settled run still polled: %#v, %v", runs, err)
	}
}
