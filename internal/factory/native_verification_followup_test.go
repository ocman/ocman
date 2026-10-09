package factory

import (
	"errors"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
)

func TestVerificationFollowUpsRunWithoutApprovalAndReverify(t *testing.T) {
	db, err := state.Open(statetest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	launcher := &fakeImplementationLauncher{store: db}
	svc := NewNativeWithExecution(db, testProjectResolver{root: "/repo"}, &fakePlanningLauncher{}, launcher)
	epic, err := svc.CreateWorkEpic(t.Context(), CreateWorkEpicRequest{Goal: "Follow up", InitialProject: "/repo", AcknowledgeLocalExecution: true})
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
		t.Helper()
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
	phase := pouredIssueID(t, svc, epic.ID, "phase")
	batch := GraphMutation{Action: "batch", EpicID: epic.ID, RationaleMarkdown: "Criterion 2 fails: missing case.", Mutations: []GraphMutation{{Action: "create", ParentID: phase, Kind: "task", Title: "Cover missing case"}}}

	// Credentials gate the bypass: a non-validator or a wrong token is refused.
	if _, err := svc.AddVerificationFollowUps(t.Context(), verification.AttemptID, "wrong", batch); !errors.Is(err, ErrActionNotPermitted) {
		t.Fatalf("wrong token = %v", err)
	}
	if _, err := svc.AddVerificationFollowUps(t.Context(), first.AttemptID, first.AgentToken, batch); err == nil {
		t.Fatal("an implementation attempt added follow-ups")
	}
	deleteBatch := GraphMutation{Action: "batch", EpicID: epic.ID, RationaleMarkdown: "x", Mutations: []GraphMutation{{Action: "delete", IssueID: phase}}}
	if _, err := svc.AddVerificationFollowUps(t.Context(), verification.AttemptID, verification.AgentToken, deleteBatch); err == nil {
		t.Fatal("a validator deleted work")
	}

	created, err := svc.AddVerificationFollowUps(t.Context(), verification.AttemptID, verification.AgentToken, batch)
	if err != nil || len(created) != 1 || !strings.HasPrefix(created[0], phase+".") {
		t.Fatalf("follow-ups = %v, %v", created, err)
	}
	detail, err := svc.GetWorkEpic(t.Context(), epic.ID)
	if err != nil || detail.PlanGate.Resolution != "approved" {
		t.Fatalf("follow-ups reopened approval: %#v, %v", detail.PlanGate, err)
	}
	if err := svc.CompleteAttempt(t.Context(), verification.AttemptID, verification.AgentToken, "late pass", ""); err == nil {
		t.Fatal("the ended verification attempt still completed")
	}
	followUp := next(2)
	if followUp.Title != "Cover missing case" || followUp.Verification {
		t.Fatalf("follow-up did not run: %#v", followUp)
	}
	if err := svc.CompleteAttempt(t.Context(), followUp.AttemptID, followUp.AgentToken, "fixed", ""); err != nil {
		t.Fatal(err)
	}
	if again := next(3); !again.Verification || again.AttemptID == verification.AttemptID {
		t.Fatalf("no fresh verification: %#v", again)
	}
	comments, err := svc.ListIssueComments(t.Context(), epic.ID, verification.WorkID)
	if err != nil || len(comments) != 1 || !strings.Contains(comments[0].Body, "Criterion 2 fails") {
		t.Fatalf("verification comment = %#v, %v", comments, err)
	}
}
