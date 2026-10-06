package factory

import (
	"testing"

	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
)

func TestGraphAmendmentDuringVerificationRunsNewWorkAndReverifies(t *testing.T) {
	db, err := state.Open(statetest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	launcher := &fakeImplementationLauncher{store: db}
	svc := NewNativeWithExecution(db, testProjectResolver{root: "/repo"}, &fakePlanningLauncher{}, launcher)
	epic, err := svc.CreateWorkEpic(t.Context(), CreateWorkEpicRequest{Goal: "Amend during verification", InitialProject: "/repo", AcknowledgeLocalExecution: true})
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := svc.SubmitProposal(t.Context(), SubmitProposalRequest{Import: true, EpicID: epic.ID, Manifest: ProposalManifest{EpicID: epic.ID, MolID: pouredIssueID(t, svc, epic.ID, "mol"), Project: "/repo", Nodes: []ManifestNode{{Key: "first", Type: "implementation", Title: "First", Requirement: "required", AcceptanceCriteria: []string{"done"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DecidePlanGate(t.Context(), epic.ID, "approve", PlanGateDecisionRequest{ExpectedRevision: proposal.Revision, ExpectedHash: proposal.ContentHash}); err != nil {
		t.Fatal(err)
	}
	next := func(index int) ImplementationSessionRequest {
		if err := svc.Dispatch(t.Context()); err != nil {
			t.Fatal(err)
		}
		if len(launcher.prompts) != index+1 {
			t.Fatalf("step %d: %#v", index, launcher.prompts)
		}
		return launcher.prompts[index]
	}
	first := next(0)
	if err := svc.CompleteAttempt(t.Context(), first.AttemptID, first.AgentToken, "first done", ""); err != nil {
		t.Fatal(err)
	}
	verification := next(1)
	if !verification.Verification {
		t.Fatal("verification did not start")
	}
	issues, err := svc.ListIssues(t.Context(), epic.ID)
	if err != nil {
		t.Fatal(err)
	}
	var phase string
	for _, issue := range issues {
		if issue.Kind == "phase" {
			phase = issue.ID
			if issue.Status != "closed" {
				t.Fatal("implementation group is not complete")
			}
		}
	}
	if phase == "" {
		t.Fatal("implementation group missing")
	}
	if err := svc.MutateGraph(t.Context(), GraphMutation{Actor: "mcp", Action: "create", EpicID: epic.ID, ParentID: phase, Kind: "task", Title: "New regression", Description: "Acceptance criteria:\n- [ ] Missing case is checked"}); err != nil {
		t.Fatalf("cannot propose a gap during verification: %v", err)
	}
	detail, err := svc.GetWorkEpic(t.Context(), epic.ID)
	if err != nil || detail.PlanGate.Resolution != "open" {
		t.Fatalf("amendment = %#v, %v", detail, err)
	}
	if err := svc.CompleteAttempt(t.Context(), verification.AttemptID, verification.AgentToken, "old scope verified", ""); err != nil {
		t.Fatal(err)
	}
	if err := svc.Dispatch(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(launcher.prompts) != 2 {
		t.Fatal("unapproved amendment dispatched")
	}
	if _, err := svc.DecidePlanGate(t.Context(), epic.ID, "approve", PlanGateDecisionRequest{ExpectedRevision: detail.PlanGate.ProposalRevision, ExpectedHash: detail.PlanGate.ProposalHash}); err != nil {
		t.Fatal(err)
	}
	newWork := next(2)
	if newWork.Title != "New regression" || newWork.Verification || newWork.Delivery {
		t.Fatalf("amendment skipped new work: %#v", newWork)
	}
	if err := svc.CompleteAttempt(t.Context(), newWork.AttemptID, newWork.AgentToken, "new work done", ""); err != nil {
		t.Fatal(err)
	}
	// The fake checkpoint stays identical: changed scope still needs a fresh review.
	newVerification := next(3)
	if !newVerification.Verification || newVerification.AttemptID == verification.AttemptID {
		t.Fatalf("delivery reused stale verification: %#v", newVerification)
	}
	if err := svc.CompleteAttempt(t.Context(), newVerification.AttemptID, newVerification.AgentToken, "new scope verified", ""); err != nil {
		t.Fatal(err)
	}
	if delivery := next(4); !delivery.Delivery {
		t.Fatalf("delivery did not follow fresh verification: %#v", delivery)
	}
}
