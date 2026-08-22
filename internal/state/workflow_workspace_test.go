package state

import "testing"

func startWithWorkspace(t *testing.T, db *DB, node string, req WorkflowWorkspaceRequest) bool {
	t.Helper()
	resources := []WorkflowResourceRequest{{Pool: "", Units: 1, Capacity: 2}}
	started, err := db.StartWorkflowCommand(t.Context(), "run-1", node, resources, &req, 10)
	if err != nil {
		t.Fatalf("start %s: %v", node, err)
	}
	return started
}

func TestWorkspaceExactOverlapBlocks(t *testing.T) {
	db := seedResourceRun(t)
	if !startWithWorkspace(t, db, "one", WorkflowWorkspaceRequest{Shards: 1, Mode: workspacePath, Paths: []string{"src/app"}}) {
		t.Fatal("first path lease should acquire")
	}
	if startWithWorkspace(t, db, "two", WorkflowWorkspaceRequest{Shards: 1, Mode: workspacePath, Paths: []string{"src/app"}}) {
		t.Fatal("exact-overlap path lease must not share the only shard")
	}
}

func TestWorkspaceAncestorOverlapBlocks(t *testing.T) {
	db := seedResourceRun(t)
	if !startWithWorkspace(t, db, "one", WorkflowWorkspaceRequest{Shards: 1, Mode: workspacePath, Paths: []string{"src"}}) {
		t.Fatal("ancestor path lease should acquire")
	}
	if startWithWorkspace(t, db, "two", WorkflowWorkspaceRequest{Shards: 1, Mode: workspacePath, Paths: []string{"src/app"}}) {
		t.Fatal("descendant path lease must not share a shard with its ancestor")
	}
}

func TestWorkspaceDisjointPathsShare(t *testing.T) {
	db := seedResourceRun(t)
	if !startWithWorkspace(t, db, "one", WorkflowWorkspaceRequest{Shards: 1, Mode: workspacePath, Paths: []string{"src/a"}}) {
		t.Fatal("first disjoint lease should acquire")
	}
	if !startWithWorkspace(t, db, "two", WorkflowWorkspaceRequest{Shards: 1, Mode: workspacePath, Paths: []string{"src/b"}}) {
		t.Fatal("disjoint path lease should share the shard")
	}
	leases, err := db.ListWorkflowWorkspaceLeases(t.Context(), "run-1")
	if err != nil || len(leases) != 2 || leases[0].Shard != leases[1].Shard {
		t.Fatalf("disjoint leases did not share one shard: %+v (%v)", leases, err)
	}
}

func TestWorkspaceExclusiveExhaustion(t *testing.T) {
	db := seedResourceRun(t)
	if !startWithWorkspace(t, db, "one", WorkflowWorkspaceRequest{Shards: 1, Mode: workspaceExclusive}) {
		t.Fatal("first exclusive lease should acquire")
	}
	if startWithWorkspace(t, db, "two", WorkflowWorkspaceRequest{Shards: 1, Mode: workspaceExclusive}) {
		t.Fatal("exclusive lease must not share; shard pool exhausted")
	}
}

func TestWorkspaceLeaseReleasedOnCompletion(t *testing.T) {
	db := seedResourceRun(t)
	if !startWithWorkspace(t, db, "one", WorkflowWorkspaceRequest{Shards: 1, Mode: workspaceExclusive}) {
		t.Fatal("acquire")
	}
	if err := db.CompleteWorkflowCommand(t.Context(), "run-1", "one", WorkflowCommandResult{State: "successful", OutputsJSON: "{}"}, 20); err != nil {
		t.Fatal(err)
	}
	leases, err := db.ListWorkflowWorkspaceLeases(t.Context(), "run-1")
	if err != nil || len(leases) != 0 {
		t.Fatalf("workspace lease not released on completion: %+v (%v)", leases, err)
	}
}

func TestWorkspaceFailureKeepsIndependentLease(t *testing.T) {
	db := seedResourceRun(t)
	if !startWithWorkspace(t, db, "one", WorkflowWorkspaceRequest{Shards: 2, Mode: workspaceExclusive}) {
		t.Fatal("acquire one")
	}
	if !startWithWorkspace(t, db, "two", WorkflowWorkspaceRequest{Shards: 2, Mode: workspaceExclusive}) {
		t.Fatal("acquire two")
	}
	// A failed branch leaves independent work running, so its lease remains.
	if err := db.CompleteWorkflowCommand(t.Context(), "run-1", "one", WorkflowCommandResult{State: "failed", OutputsJSON: "{}"}, 20); err != nil {
		t.Fatal(err)
	}
	leases, err := db.ListWorkflowWorkspaceLeases(t.Context(), "run-1")
	if err != nil || len(leases) != 1 || leases[0].NodeID != "two" {
		t.Fatalf("independent lease was not retained after failure: %+v (%v)", leases, err)
	}
}

func TestWorkspaceIdempotentReacquire(t *testing.T) {
	db := seedResourceRun(t)
	req := WorkflowWorkspaceRequest{Shards: 1, Mode: workspaceExclusive}
	if !startWithWorkspace(t, db, "one", req) {
		t.Fatal("acquire")
	}
	// A re-acquire for the same node keeps a single lease row.
	tx, err := db.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	shard, ok, err := acquireWorkflowWorkspaceLease(t.Context(), tx, "run-1", "one", 1, req, 30)
	if err != nil || !ok || shard != 0 {
		t.Fatalf("idempotent re-acquire: shard=%d ok=%v err=%v", shard, ok, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	leases, err := db.ListWorkflowWorkspaceLeases(t.Context(), "run-1")
	if err != nil || len(leases) != 1 {
		t.Fatalf("re-acquire duplicated lease: %+v (%v)", leases, err)
	}
}

func TestWorkspaceShardPathRecordedAndDurable(t *testing.T) {
	db := seedResourceRun(t)
	if !startWithWorkspace(t, db, "one", WorkflowWorkspaceRequest{Shards: 1, Mode: workspaceExclusive, Host: "host-a"}) {
		t.Fatal("acquire")
	}
	if err := db.SetWorkflowWorkspaceShardPath(t.Context(), "run-1", "one", "/tmp/shard-0"); err != nil {
		t.Fatal(err)
	}
	leases, err := db.ListWorkflowWorkspaceLeases(t.Context(), "run-1")
	if err != nil || len(leases) != 1 || leases[0].ShardPath != "/tmp/shard-0" || leases[0].Host != "host-a" {
		t.Fatalf("shard path/host not recorded: %+v (%v)", leases, err)
	}
}
