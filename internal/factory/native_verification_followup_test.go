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
	reference := GraphMutation{Action: "batch", EpicID: epic.ID, RationaleMarkdown: "x", Mutations: []GraphMutation{{Action: "create", ParentID: phase, Kind: "task", Title: "Never runs", Requirement: "reference"}}}
	if _, err := svc.AddVerificationFollowUps(t.Context(), verification.AttemptID, verification.AgentToken, reference); err == nil {
		t.Fatal("a reference follow-up was accepted")
	}
	// A failed stop leaves the validator owning the workspace and adds nothing.
	launcher.stopErr = errors.New("abort failed")
	if _, err := svc.AddVerificationFollowUps(t.Context(), verification.AttemptID, verification.AgentToken, batch); err == nil {
		t.Fatal("follow-ups committed after a failed stop")
	}
	launcher.stopErr = nil
	if issues, _ := svc.ListIssues(t.Context(), epic.ID); countTitled(issues, "Never runs")+countTitled(issues, "Cover missing case") != 0 {
		t.Fatal("rejected follow-ups were inserted")
	}
	deleteBatch := GraphMutation{Action: "batch", EpicID: epic.ID, RationaleMarkdown: "x", Mutations: []GraphMutation{{Action: "delete", IssueID: phase}}}
	if _, err := svc.AddVerificationFollowUps(t.Context(), verification.AttemptID, verification.AgentToken, deleteBatch); err == nil {
		t.Fatal("a validator deleted work")
	}

	stops := len(launcher.stops)
	created, err := svc.AddVerificationFollowUps(t.Context(), verification.AttemptID, verification.AgentToken, batch)
	if err != nil || len(created) != 1 || !strings.HasPrefix(created[0], phase+".") {
		t.Fatalf("follow-ups = %v, %v", created, err)
	}
	if len(launcher.stops) != stops+1 {
		t.Fatalf("validator was not stopped: %#v", launcher.stops)
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

func countTitled(issues []Issue, title string) int {
	n := 0
	for _, issue := range issues {
		if issue.Title == title {
			n++
		}
	}
	return n
}

func TestVerificationFollowUpsDefaultToTheVerifiedProject(t *testing.T) {
	db, err := state.Open(statetest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	launcher := &fakeImplementationLauncher{store: db}
	svc := NewNativeWithExecution(db, testProjectResolver{roots: map[string]string{"/repo": "/repo", "/other": "/other"}}, &fakePlanningLauncher{}, launcher)
	epic, err := svc.CreateWorkEpic(t.Context(), CreateWorkEpicRequest{Goal: "Two repos", InitialProject: "/repo", AcknowledgeLocalExecution: true, Projects: []ProjectAdmission{{Path: "/other", AcknowledgeLocalExecution: true}}})
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := svc.SubmitProposal(t.Context(), SubmitProposalRequest{Import: true, EpicID: epic.ID, Manifest: ProposalManifest{EpicID: epic.ID, MolID: pouredIssueID(t, svc, epic.ID, "mol"), Project: "/repo", Nodes: []ManifestNode{{Key: "repo", Type: "implementation", Requirement: "required", AcceptanceCriteria: []string{"done"}, Project: "/repo"}, {Key: "other", Type: "implementation", Requirement: "required", AcceptanceCriteria: []string{"done"}, Project: "/other"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DecidePlanGate(t.Context(), epic.ID, "approve", PlanGateDecisionRequest{ExpectedRevision: proposal.Revision, ExpectedHash: proposal.ContentHash}); err != nil {
		t.Fatal(err)
	}
	var verification ImplementationSessionRequest
	for index := 0; verification.AttemptID == "" && index < 6; index++ {
		if err := svc.Dispatch(t.Context()); err != nil {
			t.Fatal(err)
		}
		if len(launcher.prompts) <= index {
			t.Fatalf("step %d did not start", index)
		}
		request := launcher.prompts[index]
		if request.Verification && request.Repository == "/other" {
			verification = request
			break
		}
		if err := svc.CompleteAttempt(t.Context(), request.AttemptID, request.AgentToken, "done", ""); err != nil {
			t.Fatal(err)
		}
	}
	if verification.AttemptID == "" {
		t.Fatal("no verification for /other")
	}
	batch := GraphMutation{Action: "batch", EpicID: epic.ID, RationaleMarkdown: "Gap in /other.", Mutations: []GraphMutation{{Action: "create", ParentID: pouredIssueID(t, svc, epic.ID, "phase"), Kind: "task", Title: "Fix other"}}}
	if _, err := svc.AddVerificationFollowUps(t.Context(), verification.AttemptID, verification.AgentToken, batch); err != nil {
		t.Fatal(err)
	}
	issues, err := svc.ListIssues(t.Context(), epic.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, issue := range issues {
		if issue.Title == "Fix other" && issue.Project != "/other" {
			t.Fatalf("follow-up targets %q, want the verified project", issue.Project)
		}
	}
}
