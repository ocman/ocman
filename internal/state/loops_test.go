package state

import (
	"testing"
	"time"
)

func sampleLoop(id string) Loop {
	now := time.Now().UnixMilli()
	return Loop{
		ID:              id,
		Platform:        "opencode",
		RootSessionID:   "sess_root",
		Directory:       "/src/ocman",
		ProjectName:     "ocman",
		Title:           "Watch PR #42",
		Pattern:         "pr_address",
		TriggerType:     "pr_event",
		TriggerConfig:   `{"pr_number":42}`,
		ActionType:      "prompt_root",
		ActionTemplate:  "address comments",
		Model:           "anthropic/claude-sonnet-4",
		Agent:           "plan",
		Reasoning:       "high",
		PermissionRules: `[{"permission":"edit","pattern":"**","action":"deny"}]`,
		StopConditions:  `{"max_iterations":25,"max_cost_usd":5}`,
		State:           "active",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
}

func TestInsertGetLoop_RoundTrip(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	l := sampleLoop("loop_1")
	if err := db.InsertLoop(l); err != nil {
		t.Fatalf("InsertLoop: %v", err)
	}
	got, err := db.GetLoop("loop_1")
	if err != nil {
		t.Fatalf("GetLoop: %v", err)
	}
	if got.RootSessionID != "sess_root" || got.TriggerType != "pr_event" {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	if got.TriggerConfig != `{"pr_number":42}` {
		t.Fatalf("trigger_config not preserved: %q", got.TriggerConfig)
	}
	if got.Model != "anthropic/claude-sonnet-4" {
		t.Fatalf("model not preserved: %q", got.Model)
	}
	if got.Agent != "plan" || got.Reasoning != "high" {
		t.Fatalf("agent/reasoning not preserved: agent=%q reasoning=%q", got.Agent, got.Reasoning)
	}
	if got.PermissionRules != `[{"permission":"edit","pattern":"**","action":"deny"}]` {
		t.Fatalf("permission_rules not preserved: %q", got.PermissionRules)
	}
	if got.ParentLoopID != "" {
		t.Fatalf("expected empty parent_loop_id, got %q", got.ParentLoopID)
	}
	if got.CompletedAt != 0 {
		t.Fatalf("expected completed_at 0, got %d", got.CompletedAt)
	}
}

func TestUpdateLoop_AndState(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	l := sampleLoop("loop_2")
	if err := db.InsertLoop(l); err != nil {
		t.Fatalf("InsertLoop: %v", err)
	}
	l.Iteration = 3
	l.CostUSD = 1.25
	l.CurrentTask = "addressing review"
	l.Model = "openai/gpt-5.5"
	if err := db.UpdateLoop(l); err != nil {
		t.Fatalf("UpdateLoop: %v", err)
	}
	got, _ := db.GetLoop("loop_2")
	if got.Iteration != 3 || got.CostUSD != 1.25 || got.CurrentTask != "addressing review" || got.Model != "openai/gpt-5.5" {
		t.Fatalf("update not persisted: %+v", got)
	}

	if err := db.SetLoopState("loop_2", "completed", "done: 3 comments addressed"); err != nil {
		t.Fatalf("SetLoopState: %v", err)
	}
	got, _ = db.GetLoop("loop_2")
	if got.State != "completed" {
		t.Fatalf("expected completed, got %q", got.State)
	}
	if got.CompletedAt == 0 {
		t.Fatalf("expected completed_at to be stamped on terminal state")
	}
	if got.LastSummary != "done: 3 comments addressed" {
		t.Fatalf("expected summary persisted, got %q", got.LastSummary)
	}
}

func TestListActiveLoops_FiltersTerminal(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	a := sampleLoop("loop_a")
	b := sampleLoop("loop_b")
	if err := db.InsertLoop(a); err != nil {
		t.Fatalf("InsertLoop a: %v", err)
	}
	if err := db.InsertLoop(b); err != nil {
		t.Fatalf("InsertLoop b: %v", err)
	}
	if err := db.SetLoopState("loop_b", "stopped", ""); err != nil {
		t.Fatalf("SetLoopState: %v", err)
	}
	active, err := db.ListActiveLoops()
	if err != nil {
		t.Fatalf("ListActiveLoops: %v", err)
	}
	if len(active) != 1 || active[0].ID != "loop_a" {
		t.Fatalf("expected only loop_a active, got %+v", active)
	}
}

func TestLoopIterations_PendingOutbox(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	if err := db.InsertLoop(sampleLoop("loop_x")); err != nil {
		t.Fatalf("InsertLoop: %v", err)
	}
	now := time.Now().UnixMilli()
	id, err := db.InsertLoopIteration(LoopIteration{
		LoopID:         "loop_x",
		Seq:            1,
		FiredAt:        now,
		StartedAt:      now,
		RenderedPrompt: "do the thing",
		Outcome:        "pending",
	})
	if err != nil {
		t.Fatalf("InsertLoopIteration: %v", err)
	}
	if err := db.UpdateLoopIteration(LoopIteration{
		ID:              id,
		CompletedAt:     now + 100,
		TargetSessionID: "sess_root",
		Outcome:         "ok",
		Summary:         "sent",
	}); err != nil {
		t.Fatalf("UpdateLoopIteration: %v", err)
	}
	its, err := db.ListLoopIterations("loop_x")
	if err != nil {
		t.Fatalf("ListLoopIterations: %v", err)
	}
	if len(its) != 1 || its[0].Outcome != "ok" || its[0].TargetSessionID != "sess_root" {
		t.Fatalf("iteration not closed out: %+v", its)
	}
}

func TestChildSession_LoopIDLink(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()

	if err := db.InsertLoop(sampleLoop("loop_link")); err != nil {
		t.Fatalf("InsertLoop: %v", err)
	}
	if err := db.InsertChildSession(ChildSession{
		ID:              "child_1",
		Platform:        "opencode",
		ParentSessionID: "sess_root",
		Intent:          "implement",
		Status:          "running",
		CreatedAt:       time.Now().UnixMilli(),
		LoopID:          "loop_link",
	}); err != nil {
		t.Fatalf("InsertChildSession: %v", err)
	}
	// Non-loop child stays unlinked (backward compat).
	if err := db.InsertChildSession(ChildSession{
		ID:              "child_2",
		Platform:        "opencode",
		ParentSessionID: "sess_root",
		Intent:          "other",
		Status:          "running",
		CreatedAt:       time.Now().UnixMilli(),
	}); err != nil {
		t.Fatalf("InsertChildSession: %v", err)
	}

	byLoop, err := db.ListChildSessionsByLoop("loop_link")
	if err != nil {
		t.Fatalf("ListChildSessionsByLoop: %v", err)
	}
	if len(byLoop) != 1 || byLoop[0].ID != "child_1" {
		t.Fatalf("expected only child_1 linked, got %+v", byLoop)
	}

	got, err := db.GetChildSession("child_2")
	if err != nil {
		t.Fatalf("GetChildSession: %v", err)
	}
	if got.LoopID != "" {
		t.Fatalf("expected child_2 unlinked, got loop_id %q", got.LoopID)
	}
}
