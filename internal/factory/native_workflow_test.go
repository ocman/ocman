package factory

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/factory/model"
	"github.com/NoUseFreak/ocman/internal/state"
)

func TestWorkflowChecksEveryChangedProjectBeforeDelivery(t *testing.T) {
	db, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	launcher := &fakeImplementationLauncher{store: db}
	svc := NewNativeWithExecution(db, testProjectResolver{roots: map[string]string{"/repo": "/repo", "/other": "/other", "/unused": "/unused"}}, &fakePlanningLauncher{}, launcher)
	epic, err := svc.CreateWorkEpic(t.Context(), CreateWorkEpicRequest{Goal: "Both projects", InitialProject: "/repo", AcknowledgeLocalExecution: true, Projects: []ProjectAdmission{{Path: "/other", AcknowledgeLocalExecution: true}, {Path: "/unused", AcknowledgeLocalExecution: true}}})
	if err != nil {
		t.Fatal(err)
	}
	if epic.FormulaVersion != 3 {
		t.Fatal("new epics do not use YAML")
	}
	if _, err := svc.Pour(t.Context(), epic.ID); err != nil {
		t.Fatal(err)
	}
	proposal, err := svc.SubmitProposal(t.Context(), SubmitProposalRequest{EpicID: epic.ID, Manifest: ProposalManifest{EpicID: epic.ID, MolID: pouredIssueID(t, svc, epic.ID, "mol"), Project: "/repo", Nodes: []ManifestNode{{Key: "repo", Type: "implementation", Requirement: "required", Project: "/repo"}, {Key: "other", Type: "implementation", Requirement: "required", Project: "/other"}, {Key: "optional", Type: "implementation", Requirement: "optional", Project: "/repo"}, {Key: "unused", Type: "implementation", Requirement: "optional", Project: "/unused"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DecidePlanGate(t.Context(), epic.ID, "approve", PlanGateDecisionRequest{ExpectedRevision: proposal.Revision, ExpectedHash: proposal.ContentHash}); err != nil {
		t.Fatal(err)
	}
	issues, err := svc.ListIssues(t.Context(), epic.ID)
	if err != nil {
		t.Fatal(err)
	}
	deferred := ""
	for _, issue := range issues {
		if issue.Kind == "implementation" && issue.Project == "/unused" {
			deferred = issue.ID
			if err := svc.DeferIssue(t.Context(), epic.ID, issue.ID, "Skip optional project"); err != nil {
				t.Fatal(err)
			}
		}
	}
	if deferred == "" {
		t.Fatal("missing optional work")
	}
	verified := map[string]bool{}
	delivered := map[string]bool{}
	for index := range 7 {
		launcher.result = PlanningSession{Platform: "opencode", ID: fmt.Sprintf("session-%d", index)}
		if err := svc.Dispatch(t.Context()); err != nil {
			t.Fatal(err)
		}
		if len(launcher.prompts) != index+1 {
			t.Fatalf("step %d did not start: %#v", index, launcher.prompts)
		}
		request := launcher.prompts[index]
		if request.Repository == "/unused" {
			t.Fatal("deferred optional project got mandatory work")
		}
		if request.Verification != (index >= 3 && index < 5) || request.Delivery != (index >= 5) {
			t.Fatalf("wrong step order: %#v", request)
		}
		if request.Verification {
			verified[request.Repository] = true
		}
		pr := ""
		if request.Delivery {
			if len(verified) != 2 {
				t.Fatal("delivery bypassed a project check")
			}
			delivered[request.Repository] = true
			pr = fmt.Sprintf("https://forge.example/pull/%d", index)
		}
		if err := svc.CompleteAttempt(t.Context(), request.AttemptID, request.AgentToken, "passed", pr); err != nil {
			t.Fatal(err)
		}
	}
	if len(delivered) != 2 {
		t.Fatalf("deliveries = %#v", delivered)
	}
	if err := svc.Dispatch(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(launcher.prompts) != 7 {
		t.Fatal("completed steps ran twice")
	}
	if err := svc.ResumeIssue(t.Context(), epic.ID, deferred); err == nil {
		t.Fatal("optional work resumed after final delivery")
	}
}

func TestCompileWorkflow(t *testing.T) {
	compiled, err := compileNativeFormula(tracerWorkflowSource)
	if err != nil {
		t.Fatal(err)
	}
	if len(compiled.Steps) != 5 || compiled.Steps["implement"].Prompt == "" || compiled.Steps["verify"].Needs[0] != "implement" {
		t.Fatalf("compiled = %#v", compiled)
	}
	for _, tc := range []struct{ name, source string }{
		{"unknown field", strings.Replace(tracerWorkflowSource, "name: Tracer", "name: Tracer\nunexpected: true", 1)},
		{"duplicate", strings.Replace(tracerWorkflowSource, "name: Tracer", "name: Tracer\nname: Other", 1)},
		{"unknown dependency", strings.Replace(tracerWorkflowSource, "needs: [implement]", "needs: [missing]", 1)},
		{"cycle", strings.Replace(tracerWorkflowSource, "needs: [approve]", "needs: [verify]", 1)},
		{"missing implementation", strings.Replace(tracerWorkflowSource, "kind: implementation", "kind: verification", 1)},
		{"unsupported concurrency", strings.Replace(tracerWorkflowSource, "concurrency: 1", "concurrency: 2", 1)},
		{"bypass verification", strings.Replace(tracerWorkflowSource, "needs: [verify]", "needs: [implement]", 1)},
		{"multiple documents", tracerWorkflowSource + "\n---\nversion: 2\n"},
		{"unknown kind", strings.Replace(tracerWorkflowSource, "kind: verification", "kind: magic", 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := compileNativeFormula(tc.source); err == nil {
				t.Fatal("accepted invalid workflow")
			}
		})
	}
	reformatted, err := compileNativeFormula("# formatting does not change identity\n" + tracerWorkflowSource)
	if err != nil || reformatted.Hash == compiled.Hash {
		t.Fatalf("source-only edits must create a new revision: %v", err)
	}
}

func TestWorkflowScopeExpansionFromVerification(t *testing.T) {
	db, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	launcher := &fakeImplementationLauncher{store: db}
	planning := &fakePlanningLauncher{result: PlanningSession{Platform: "opencode", ID: "scope-plan"}}
	svc := NewNativeWithExecution(db, testProjectResolver{roots: map[string]string{"/repo": "/repo", "/other": "/other"}}, planning, launcher)
	epic, err := svc.CreateWorkEpic(t.Context(), CreateWorkEpicRequest{Goal: "Expand during checks", InitialProject: "/repo", AcknowledgeLocalExecution: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Pour(t.Context(), epic.ID); err != nil {
		t.Fatal(err)
	}
	proposal, err := svc.SubmitProposal(t.Context(), SubmitProposalRequest{EpicID: epic.ID, Manifest: ProposalManifest{EpicID: epic.ID, MolID: pouredIssueID(t, svc, epic.ID, "mol"), Project: "/repo", Nodes: []ManifestNode{{Key: "one", Type: "implementation", Requirement: "required"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DecidePlanGate(t.Context(), epic.ID, "approve", PlanGateDecisionRequest{ExpectedRevision: proposal.Revision, ExpectedHash: proposal.ContentHash}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Dispatch(t.Context()); err != nil {
		t.Fatal(err)
	}
	work := launcher.prompts[0]
	if err := svc.CompleteAttempt(t.Context(), work.AttemptID, work.AgentToken, "done", ""); err != nil {
		t.Fatal(err)
	}
	launcher.result = PlanningSession{Platform: "opencode", ID: "check"}
	if err := svc.Dispatch(t.Context()); err != nil {
		t.Fatal(err)
	}
	check := launcher.prompts[1]
	if !check.Verification {
		t.Fatal("verification did not start")
	}
	gate, err := svc.RequestProject(t.Context(), check.AttemptID, check.AgentToken, "/other", "Integration needs the other repository")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ResolveProjectRequest(t.Context(), gate.IssueID, "approve", "", true); err != nil {
		t.Fatal(err)
	}
	if len(planning.prompts) != 1 {
		t.Fatalf("scope planner = %#v", planning.prompts)
	}
	plan := planning.prompts[0]
	phase := pouredIssueID(t, svc, epic.ID, "phase")
	if _, err := svc.SubmitScopePlan(t.Context(), SubmitProposalRequest{EpicID: epic.ID, AttemptID: plan.AttemptID, AttemptToken: plan.AgentToken, Manifest: ProposalManifest{EpicID: epic.ID, MolID: phase, Project: "/repo", Nodes: []ManifestNode{{Key: "other", Type: "implementation", Requirement: "required", Project: "/other"}}}}); err != nil {
		t.Fatal(err)
	}
	launcher.result = PlanningSession{Platform: "opencode", ID: "other-work"}
	if err := svc.Dispatch(t.Context()); err != nil {
		t.Fatal(err)
	}
	issues, err := svc.ListIssues(t.Context(), epic.ID)
	if err != nil {
		t.Fatal(err)
	}
	checks, deliveries := 0, 0
	for _, issue := range issues {
		if issue.Project != "/other" || issue.Workflow == nil {
			continue
		}
		if issue.Workflow.Kind == "verification" {
			checks++
		}
		if issue.Workflow.Kind == "delivery" {
			deliveries++
		}
	}
	if checks != 1 || deliveries != 1 {
		t.Fatalf("new project got %d checks and %d deliveries", checks, deliveries)
	}
}

func TestWorkflowImplementationBarrierAndPostChecks(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "state.db")
	db, err := state.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	launcher := &fakeImplementationLauncher{store: db}
	svc := NewNativeWithExecution(db, testProjectResolver{root: "/repo"}, &fakePlanningLauncher{}, launcher)
	source := strings.Replace(tracerWorkflowSource, "needs: [verify]", "needs: [release]", 1) + "\n  release:\n    kind: approval\n    needs: [verify]\n"
	source = strings.Replace(source, "needs: [approve]", "needs: [security]", 1) + "\n  security:\n    kind: approval\n    needs: [approve]\n"
	source = strings.Replace(source, "  verify:\n    kind: verification", "  verify:\n    kind: verification\n    config:\n      model: test/reviewer", 1)
	formula, err := svc.SaveFormula(t.Context(), FormulaSaveRequest{ID: "custom/workflow", Source: source})
	if err != nil {
		t.Fatal(err)
	}
	commented, err := svc.SaveFormula(t.Context(), FormulaSaveRequest{ID: formula.ID, Source: source + "\n# Preserve this explanation.\n"})
	if err != nil || commented.Version == formula.Version || commented.Source != source+"\n# Preserve this explanation.\n" {
		t.Fatalf("comment edit was lost: %#v, %v", commented, err)
	}
	epic, err := svc.CreateWorkEpic(t.Context(), CreateWorkEpicRequest{Goal: "Ship", InitialProject: "/repo", AcknowledgeLocalExecution: true, FormulaID: formula.ID, FormulaRevision: formula.Version})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Pour(t.Context(), epic.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SubmitProposal(t.Context(), SubmitProposalRequest{EpicID: epic.ID, Manifest: ProposalManifest{EpicID: epic.ID, MolID: pouredIssueID(t, svc, epic.ID, "mol"), Project: "/repo", Nodes: []ManifestNode{{Key: "work", Type: "implementation", Requirement: "required"}, {Key: "delivery", Type: "delivery", Requirement: "required"}}}}); err == nil {
		t.Fatal("accepted a delivery node into a workflow plan")
	}
	proposal, err := svc.SubmitProposal(t.Context(), SubmitProposalRequest{EpicID: epic.ID, Manifest: ProposalManifest{EpicID: epic.ID, MolID: pouredIssueID(t, svc, epic.ID, "mol"), Project: "/repo", Nodes: []ManifestNode{{Key: "one", Type: "implementation", Requirement: "required"}, {Key: "two", Type: "implementation", Requirement: "required"}}}})
	if err != nil {
		t.Fatal(err)
	}
	phaseID := pouredIssueID(t, svc, epic.ID, "materialization")
	if _, err := svc.DecidePlanGate(t.Context(), epic.ID, "approve", PlanGateDecisionRequest{ExpectedRevision: proposal.Revision, ExpectedHash: proposal.ContentHash}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Materialize(t.Context(), epic.ID, phaseID); err != nil {
		t.Fatal(err)
	}
	approvalID := func(key string) string {
		issues, err := svc.ListIssues(t.Context(), epic.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, issue := range issues {
			if issue.Workflow != nil && issue.Workflow.Key == key {
				return issue.ID
			}
		}
		t.Fatalf("missing step %s", key)
		return ""
	}
	if err := svc.Dispatch(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(launcher.prompts) != 0 {
		t.Fatal("implementation bypassed the second approval")
	}
	if err := svc.MutateGraph(t.Context(), model.GraphMutation{EpicID: epic.ID, IssueID: approvalID("security"), Action: "approve_step", Actor: "user"}); err != nil {
		t.Fatal(err)
	}
	for index, session := range []string{"one", "two", "verification"} {
		launcher.result = PlanningSession{Platform: "opencode", ID: session}
		if err := svc.Dispatch(t.Context()); err != nil {
			t.Fatal(err)
		}
		if len(launcher.prompts) != index+1 {
			t.Fatalf("dispatch %d = %#v", index, launcher.prompts)
		}
		request := launcher.prompts[index]
		if request.Delivery || request.Verification != (index == 2) {
			t.Fatalf("wrong phase dispatched: %#v", request)
		}
		if index == 2 {
			if request.Model != "test/reviewer" {
				t.Fatalf("verification model = %q", request.Model)
			}
			gate, err := svc.CreateRecoveryGate(t.Context(), request.AttemptID, request.AgentToken, "Checks failed", "test failed", []string{"retry"})
			if err != nil {
				t.Fatal(err)
			}
			if err := svc.Dispatch(t.Context()); err != nil {
				t.Fatal(err)
			}
			if len(launcher.prompts) != 3 {
				t.Fatal("delivery bypassed failed verification")
			}
			if _, err := svc.ResolveRecoveryGate(t.Context(), gate.IssueID, "resume", "Checks fixed; rerun them"); err != nil {
				t.Fatal(err)
			}
		}
		if err := svc.CompleteAttempt(t.Context(), request.AttemptID, request.AgentToken, "checks passed", ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := svc.Dispatch(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(launcher.prompts) != 3 {
		t.Fatal("delivery bypassed manual approval")
	}
	approval := approvalID("release")
	if err := svc.MutateGraph(t.Context(), model.GraphMutation{EpicID: epic.ID, IssueID: approval, Action: "approve_step", Actor: "mcp"}); err == nil {
		t.Fatal("agent approved a human gate")
	}
	if err := svc.MutateGraph(t.Context(), model.GraphMutation{EpicID: epic.ID, IssueID: approval, Action: "reject_step", Actor: "user"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Dispatch(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(launcher.prompts) != 3 {
		t.Fatal("rejected approval did not block delivery")
	}
	if err := svc.MutateGraph(t.Context(), model.GraphMutation{EpicID: epic.ID, IssueID: approval, Action: "approve_step", Actor: "user"}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = state.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	launcher.store = db
	svc = NewNativeWithExecution(db, testProjectResolver{root: "/repo"}, &fakePlanningLauncher{}, launcher)
	launcher.result = PlanningSession{Platform: "opencode", ID: "delivery"}
	if err := svc.Dispatch(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(launcher.prompts) != 4 || !launcher.prompts[3].Delivery {
		t.Fatalf("delivery = %#v", launcher.prompts)
	}
}
