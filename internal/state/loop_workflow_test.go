package state

import "testing"

func TestEnsureLoopWorkflowAndGetLoopWorkflow(t *testing.T) {
	db := openTestStateDB(t)
	t.Cleanup(func() { db.Close() })
	loop := Loop{
		ID: "loop_new", Platform: "opencode", RootSessionID: "root", Directory: "/repo",
		TriggerType: "schedule", TriggerConfig: `{"interval_seconds":60}`,
		ActionType: "prompt_root", ActionTemplate: "work", StopConditions: `{"max_cost_usd":1}`,
		State: "active", CreatedAt: 1, UpdatedAt: 1,
	}
	if err := db.InsertLoop(loop); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureLoopWorkflow(loop.ID); err != nil {
		t.Fatal(err)
	}
	mapping, err := db.GetLoopWorkflow(loop.ID)
	if err != nil {
		t.Fatal(err)
	}
	if mapping.WorkflowID != "wf_loop_loop_new" || mapping.VersionID != "wfv_loop_loop_new" || mapping.TriggerID != "trigger" {
		t.Fatalf("unexpected mapping: %+v", mapping)
	}
}
