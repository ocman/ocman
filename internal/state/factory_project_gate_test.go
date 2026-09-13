package state

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory/model"
)

func TestFactoryProjectRequestGateApprovalExpandsScopeAtomically(t *testing.T) {
	db := openTestStateDB(t)
	t.Cleanup(func() { _ = db.Close() })
	ctx := t.Context()
	epic, err := db.CreateFactoryEpic(ctx, "", "Epic", "", "/repo", "", nativeTracerFormula(t))
	if err != nil {
		t.Fatal(err)
	}
	molID := factoryIssueID(t, db, epic.ID, "mol")
	if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Action: "create", EpicID: epic.ID, ParentID: molID, Kind: "mol", Title: "Nested scope"}); err != nil {
		t.Fatal(err)
	}
	nestedMolID := issueIDWithTitle(t, db, epic.ID, "Nested scope")
	if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Action: "create", EpicID: epic.ID, ParentID: nestedMolID, Kind: "implementation", Title: "Original work"}); err != nil {
		t.Fatal(err)
	}
	workID := factoryIssueID(t, db, epic.ID, "implementation")
	attempt, err := db.CreatePreparedFactoryAttempt(ctx, epic.ID, workID, model.FactoryAttemptPolicy{Repository: "/repo", Profile: "factory-implement/v1", CheckpointSHA: "keep-me"}, time.UnixMilli(10))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec(`UPDATE factory_issue SET status = 'in_progress' WHERE id = ?`, workID); err != nil {
		t.Fatal(err)
	}
	if changed, err := db.ActivateFactoryAttempt(ctx, attempt.ID, model.PlanningSession{Platform: "opencode", ID: "session"}, time.UnixMilli(11)); err != nil || !changed {
		t.Fatalf("activate = %v, %v", changed, err)
	}

	gate, err := db.CreateFactoryProjectRequestGate(ctx, attempt.ID, "/requested", "needs shared code", time.UnixMilli(12))
	if err != nil {
		t.Fatal(err)
	}
	if duplicate, err := db.CreateFactoryProjectRequestGate(ctx, attempt.ID, "/ignored", "duplicate", time.UnixMilli(13)); err != nil || duplicate.IssueID != gate.IssueID {
		t.Fatalf("duplicate gate = %#v, %v", duplicate, err)
	}
	if got, ok, err := db.GetFactoryProjectRequestGate(ctx, gate.IssueID); err != nil || !ok || got.RequestedProject != "/requested" || got.Resolution != "open" {
		t.Fatalf("GetFactoryProjectRequestGate = %#v, %v, %v", got, ok, err)
	}
	if _, ok, err := db.GetFactoryProjectRequestGate(ctx, "missing"); err != nil || ok {
		t.Fatalf("missing gate = %v, %v", ok, err)
	}
	if paused, err := db.IsFactoryAttemptRecoveryPaused(ctx, attempt.ID); err != nil || !paused {
		t.Fatalf("project gate paused = %v, %v", paused, err)
	}
	listed := issueByID(t, db, epic.ID, gate.IssueID)
	if listed.ProjectRequestGate == nil || listed.ProjectRequestGate.RequestedProject != "/requested" {
		t.Fatalf("listed project gate = %#v", listed.ProjectRequestGate)
	}
	for _, action := range []string{"resume", "cancel"} {
		if _, _, err := db.ResolveFactoryProjectRequestGate(ctx, gate.IssueID, action, "/canonical", "", time.UnixMilli(14)); err == nil {
			t.Fatalf("%s project gate succeeded", action)
		}
	}

	resolved, _, err := db.ResolveFactoryProjectRequestGate(ctx, gate.IssueID, "approve", "/canonical", "", time.UnixMilli(15))
	if err != nil || resolved.Resolution != "approved" || resolved.CanonicalProject != "/canonical" || resolved.PlanIssueID == "" {
		t.Fatalf("approved gate = %#v, %v", resolved, err)
	}
	storedAttempt, ok, err := db.GetFactoryAttempt(ctx, attempt.ID)
	if err != nil || !ok || storedAttempt.Phase != model.FactoryAttemptTerminal || storedAttempt.Outcome != model.FactoryAttemptCancelled || storedAttempt.FrozenPolicy.CheckpointSHA != "keep-me" {
		t.Fatalf("terminated attempt = %#v, %v, %v", storedAttempt, ok, err)
	}
	original := issueByID(t, db, epic.ID, workID)
	if original.Status != "deferred" || len(original.DependsOn) != 1 || original.DependsOn[0].ID != resolved.PlanIssueID {
		t.Fatalf("deferred original = %#v", original)
	}
	plan := issueByID(t, db, epic.ID, resolved.PlanIssueID)
	if plan.Kind != "plan" || plan.Project != "/canonical" || plan.ParentID != nestedMolID || plan.Status != "open" {
		t.Fatalf("new plan = %#v", plan)
	}
	if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Action: "edit", EpicID: epic.ID, IssueID: workID, Title: "Changed"}); err == nil {
		t.Fatal("edited interrupted work during scope planning")
	}
	if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Action: "delete", EpicID: epic.ID, IssueID: nestedMolID}); err == nil {
		t.Fatal("deleted ancestor of interrupted work during scope planning")
	}
	if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Action: "edit", EpicID: epic.ID, IssueID: resolved.PlanIssueID, Title: "Changed plan"}); err == nil {
		t.Fatal("edited generated Plan during scope planning")
	}
	if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Action: "create", EpicID: epic.ID, ParentID: molID, Kind: "implementation", Title: "Unrelated work"}); err != nil {
		t.Fatal(err)
	}
	if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Action: "reparent", EpicID: epic.ID, IssueID: issueIDWithTitle(t, db, epic.ID, "Unrelated work"), ParentID: resolved.PlanIssueID}); err == nil {
		t.Fatal("reparented work beneath generated Plan")
	}
	gotEpic, err := db.GetFactoryEpic(ctx, epic.ID)
	if err != nil || len(gotEpic.Projects) != 2 || gotEpic.Projects[1].Path != "/canonical" {
		t.Fatalf("admitted projects = %#v, %v", gotEpic.Projects, err)
	}
	if _, _, err := db.ResolveFactoryProjectRequestGate(ctx, gate.IssueID, "reject", "", "", time.UnixMilli(16)); err == nil {
		t.Fatal("resolved project gate changed twice")
	}
	_, planAttempt, err := db.ClaimFactoryPlan(ctx, epic.ID, resolved.PlanIssueID, "factory-plan/v1", time.UnixMilli(17))
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := db.ActivateFactoryAttempt(ctx, planAttempt.ID, model.PlanningSession{Platform: "opencode", ID: "scope-plan"}, time.UnixMilli(18)); err != nil || !changed {
		t.Fatalf("activate scope plan = %v, %v", changed, err)
	}
	manifest, _ := json.Marshal(map[string]any{"epicId": epic.ID, "molId": nestedMolID, "project": "/repo", "nodes": []map[string]any{{"key": "blocker", "type": "implementation", "requirement": "required", "title": "New blocker", "project": "/canonical"}}})
	applied, err := db.ApplyFactoryScopePlan(ctx, model.NativeProposalRevision{EpicID: epic.ID, MolID: nestedMolID, Project: "/repo", ManifestJSON: string(manifest), ContentHash: "scope"}, planAttempt.ID, planAttempt.AgentToken, time.UnixMilli(19))
	if err != nil || applied.Revision == 0 {
		t.Fatalf("apply scope Plan = %#v, %v", applied, err)
	}
	original = issueByID(t, db, epic.ID, workID)
	if original.Status != "open" || original.DispatchState != "waiting" || len(original.DependsOn) != 2 {
		t.Fatalf("replanned original = %#v", original)
	}
	blocker := ""
	issues, err := db.ListFactoryIssues(ctx, epic.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, issue := range issues {
		if issue.Title == "New blocker" {
			blocker = issue.ID
		}
	}
	if blocker == "" {
		t.Fatal("new blocker not found")
	}
	if _, err := db.db.Exec(`UPDATE factory_issue SET status = 'closed', outcome = 'succeeded' WHERE id = ?`, blocker); err != nil {
		t.Fatal(err)
	}
	if got := issueByID(t, db, epic.ID, workID); got.DispatchState != "ready" {
		t.Fatalf("original after blocker = %#v", got)
	}
}

func TestFactoryProjectRequestGateRejectKeepsAttemptRunning(t *testing.T) {
	db := openTestStateDB(t)
	t.Cleanup(func() { _ = db.Close() })
	gate, attempt, _, _ := createActiveProjectRequestGate(t, db, "/repo")
	resolved, _, err := db.ResolveFactoryProjectRequestGate(t.Context(), gate.IssueID, "reject", "", "Continue without it", time.UnixMilli(20))
	if err != nil || resolved.Resolution != "reject_pending" {
		t.Fatalf("reject = %#v, %v", resolved, err)
	}
	resolved, err = db.CompleteFactoryProjectRequestRejection(t.Context(), gate.IssueID, time.UnixMilli(21))
	if err != nil || resolved.Resolution != "rejected" || resolved.Response != "Continue without it" {
		t.Fatalf("complete reject = %#v, %v", resolved, err)
	}
	stored, ok, err := db.GetFactoryAttempt(t.Context(), attempt.ID)
	if err != nil || !ok || stored.Phase != model.FactoryAttemptActive {
		t.Fatalf("attempt after reject = %#v, %v, %v", stored, ok, err)
	}
	if paused, err := db.IsFactoryAttemptRecoveryPaused(t.Context(), attempt.ID); err != nil || paused {
		t.Fatalf("rejected gate paused = %v, %v", paused, err)
	}
}

func TestFactoryProjectRequestGateApprovalPreservesCompletedWork(t *testing.T) {
	db := openTestStateDB(t)
	t.Cleanup(func() { _ = db.Close() })
	gate, attempt, epicID, _ := createActiveProjectRequestGate(t, db, "/repo")
	if _, err := db.db.Exec(`UPDATE factory_issue SET status = 'closed', outcome = 'succeeded' WHERE id = ?; UPDATE factory_attempt SET phase = 'terminal', terminal_outcome = 'succeeded' WHERE id = ?`, gate.WorkID, attempt.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.ResolveFactoryProjectRequestGate(t.Context(), gate.IssueID, "approve", "/canonical", "", time.Now()); err == nil {
		t.Fatal("approved gate for completed work")
	}
	stored := issueByID(t, db, epicID, gate.WorkID)
	if stored.Status != "closed" || stored.Outcome != "succeeded" {
		t.Fatalf("completed work changed = %#v", stored)
	}
	got, ok, err := db.GetFactoryProjectRequestGate(t.Context(), gate.IssueID)
	if err != nil || !ok || got.Resolution != "open" || got.PlanIssueID != "" {
		t.Fatalf("gate after rolled back approval = %#v, %v, %v", got, ok, err)
	}
	epic, err := db.GetFactoryEpic(t.Context(), epicID)
	if err != nil || len(epic.Projects) != 1 {
		t.Fatalf("projects after rolled back approval = %#v, %v", epic.Projects, err)
	}
}

func TestFactoryProjectRequestGateExcludesCapacityAndBlocksCompletion(t *testing.T) {
	db := openTestStateDB(t)
	t.Cleanup(func() { _ = db.Close() })
	gate, attempt, epicID, molID := createActiveProjectRequestGate(t, db, "/one")
	if err := db.SetFactoryCapacityPolicy(t.Context(), model.FactoryCapacityPolicy{GlobalCapacity: 1, ProjectCapacity: 1}); err != nil {
		t.Fatal(err)
	}
	second, err := db.CreateFactoryEpic(t.Context(), "", "Second", "", "/two", "", nativeTracerFormula(t))
	if err != nil {
		t.Fatal(err)
	}
	secondMol := factoryIssueID(t, db, second.ID, "mol")
	if err := db.MutateFactoryGraph(t.Context(), model.GraphMutation{Action: "create", EpicID: second.ID, ParentID: secondMol, Kind: "implementation", Title: "Work"}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertFactoryLocalExecutionAck(t.Context(), "local", "/two", "factory-implement", "v1", "operator", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.ClaimFactoryImplementation(t.Context(), second.ID, factoryIssueID(t, db, second.ID, "implementation"), "factory-implement/v1", time.Now()); err != nil {
		t.Fatalf("claim while gated attempt excludes capacity: %v", err)
	}
	if changed, err := db.CompleteFactoryAttempt(t.Context(), attempt.ID, model.FactoryAttemptResult{SchemaVersion: 1}, time.Now()); err != nil || changed {
		t.Fatalf("completion through project gate = %v, %v", changed, err)
	}
	if _, err := db.db.Exec(`UPDATE factory_issue SET status = 'closed', outcome = 'succeeded' WHERE id = ?; UPDATE factory_attempt SET phase = 'terminal', terminal_outcome = 'succeeded' WHERE id = ?`, gate.WorkID, attempt.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.CloseFactoryMol(t.Context(), epicID, molID); err == nil {
		t.Fatal("closed Mol through open project gate")
	}
	if _, err := db.db.Exec(`UPDATE factory_project_request_gate SET resolution = 'approve_pending' WHERE issue_id = ?`, gate.IssueID); err != nil {
		t.Fatal(err)
	}
	if err := db.CloseFactoryMol(t.Context(), epicID, molID); err == nil {
		t.Fatal("closed Mol through pending project gate")
	}
}

func createActiveProjectRequestGate(t *testing.T, db *DB, project string) (model.ProjectRequestGate, model.FactoryAttempt, string, string) {
	t.Helper()
	epic, err := db.CreateFactoryEpic(t.Context(), "", "Epic", "", project, "", nativeTracerFormula(t))
	if err != nil {
		t.Fatal(err)
	}
	molID := factoryIssueID(t, db, epic.ID, "mol")
	if err := db.MutateFactoryGraph(t.Context(), model.GraphMutation{Action: "create", EpicID: epic.ID, ParentID: molID, Kind: "implementation", Title: "Work"}); err != nil {
		t.Fatal(err)
	}
	workID := factoryIssueID(t, db, epic.ID, "implementation")
	attempt, err := db.CreatePreparedFactoryAttempt(t.Context(), epic.ID, workID, model.FactoryAttemptPolicy{Repository: project, Profile: "factory-implement/v1"}, time.UnixMilli(1))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec(`UPDATE factory_issue SET status = 'in_progress' WHERE id = ?`, workID); err != nil {
		t.Fatal(err)
	}
	if changed, err := db.ActivateFactoryAttempt(t.Context(), attempt.ID, model.PlanningSession{Platform: "opencode", ID: "session-" + epic.ID}, time.UnixMilli(2)); err != nil || !changed {
		t.Fatalf("activate = %v, %v", changed, err)
	}
	gate, err := db.CreateFactoryProjectRequestGate(t.Context(), attempt.ID, "/requested", "reason", time.UnixMilli(3))
	if err != nil {
		t.Fatal(err)
	}
	return gate, attempt, epic.ID, molID
}

func issueIDWithTitle(t *testing.T, db *DB, epicID, title string) string {
	t.Helper()
	issues, err := db.ListFactoryIssues(t.Context(), epicID)
	if err != nil {
		t.Fatal(err)
	}
	for _, issue := range issues {
		if issue.Title == title {
			return issue.ID
		}
	}
	t.Fatalf("missing issue %q", title)
	return ""
}
