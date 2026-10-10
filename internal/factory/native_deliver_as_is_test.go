package factory

import (
	"errors"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
)

func deliverAsIsFixture(t *testing.T) (*NativeService, *fakeImplementationLauncher, string, func(int) ImplementationSessionRequest) {
	t.Helper()
	db, err := state.Open(statetest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	launcher := &fakeImplementationLauncher{store: db}
	svc := NewNativeWithExecution(db, testProjectResolver{root: "/repo"}, &fakePlanningLauncher{}, launcher)
	epic, err := svc.CreateWorkEpic(t.Context(), CreateWorkEpicRequest{Goal: "Deliver as is", InitialProject: "/repo", AcknowledgeLocalExecution: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DeliverAsIs(t.Context(), epic.ID); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("delivered before any plan: %v", err)
	}
	proposal, err := svc.SubmitProposal(t.Context(), SubmitProposalRequest{Import: true, EpicID: epic.ID, Manifest: ProposalManifest{EpicID: epic.ID, MolID: pouredIssueID(t, svc, epic.ID, "mol"), Project: "/repo", Nodes: []ManifestNode{
		{Key: "first", Type: "implementation", Title: "First", Requirement: "required", AcceptanceCriteria: []string{"done"}},
		{Key: "second", Type: "implementation", Title: "Second", Requirement: "required", AcceptanceCriteria: []string{"done"}},
	}, Edges: []ManifestEdge{{From: "second", To: "first", Type: "blocks"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DecidePlanGate(t.Context(), epic.ID, "approve", PlanGateDecisionRequest{ExpectedRevision: proposal.Revision, ExpectedHash: proposal.ContentHash}); err != nil {
		t.Fatal(err)
	}
	return svc, launcher, epic.ID, func(index int) ImplementationSessionRequest {
		t.Helper()
		if err := svc.Dispatch(t.Context()); err != nil {
			t.Fatal(err)
		}
		if len(launcher.prompts) != index+1 {
			t.Fatalf("step %d: %#v", index, launcher.prompts)
		}
		return launcher.prompts[index]
	}
}

// The review loop: a validator proposes yet another follow-up. Delivering as
// is stops the validator, accepts and leaves out the follow-up, waives
// verification, and runs delivery listing what was left out.
func TestDeliverAsIsEndsTheReviewLoop(t *testing.T) {
	svc, launcher, epicID, next := deliverAsIsFixture(t)
	first := next(0)
	if _, err := svc.DeliverAsIs(t.Context(), epicID); err == nil || !strings.Contains(err.Error(), "wait for running work") {
		t.Fatalf("delivered while implementation runs: %v", err)
	}
	if err := svc.CompleteAttempt(t.Context(), first.AttemptID, first.AgentToken, "first done", ""); err != nil {
		t.Fatal(err)
	}
	second := next(1)
	if err := svc.CompleteAttempt(t.Context(), second.AttemptID, second.AgentToken, "second done", ""); err != nil {
		t.Fatal(err)
	}
	verification := next(2)
	if !verification.Verification {
		t.Fatal("verification did not start")
	}
	issues, err := svc.ListIssues(t.Context(), epicID)
	if err != nil {
		t.Fatal(err)
	}
	var phase string
	for _, issue := range issues {
		if issue.Kind == "phase" {
			phase = issue.ID
		}
	}
	if err := svc.MutateGraph(t.Context(), GraphMutation{Actor: "mcp", Action: "create", EpicID: epicID, ParentID: phase, Kind: "task", Title: "Yet another gap"}); err != nil {
		t.Fatal(err)
	}
	stops := len(launcher.stops)
	leftOut, err := svc.DeliverAsIs(t.Context(), epicID)
	if err != nil || len(leftOut) != 1 {
		t.Fatalf("deliver as is = %v, %v", leftOut, err)
	}
	if len(launcher.stops) != stops+1 {
		t.Fatal("the running validator was not stopped")
	}
	if err := svc.CompleteAttempt(t.Context(), verification.AttemptID, verification.AgentToken, "late verdict", ""); err == nil {
		t.Fatal("a stopped validator completed")
	}
	delivery := next(3)
	if !delivery.Delivery || !strings.Contains(delivery.LeftOut, "Yet another gap") || strings.Contains(delivery.LeftOut, "Second") {
		t.Fatalf("delivery = %#v", delivery)
	}
	detail, err := svc.GetWorkEpic(t.Context(), epicID)
	if err != nil || detail.PlanGate.Resolution != "approved" {
		t.Fatalf("plan gate = %#v, %v", detail.PlanGate, err)
	}
	if _, err := svc.DeliverAsIs(t.Context(), epicID); err == nil {
		t.Fatal("delivered as is while delivery runs")
	}
}

// Work paused for a human decision is also a way out: the paused Issue is left
// out and the never-run verification is waived.
func TestDeliverAsIsLeavesOutWorkPausedForRecovery(t *testing.T) {
	svc, launcher, epicID, next := deliverAsIsFixture(t)
	first := next(0)
	if err := svc.CompleteAttempt(t.Context(), first.AttemptID, first.AgentToken, "first done", ""); err != nil {
		t.Fatal(err)
	}
	second := next(1)
	gate, err := svc.CreateRecoveryGate(t.Context(), second.AttemptID, second.AgentToken, "Which API?", "Both change behaviour", nil)
	if err != nil {
		t.Fatal(err)
	}
	leftOut, err := svc.DeliverAsIs(t.Context(), epicID)
	if err != nil || len(leftOut) != 1 {
		t.Fatalf("deliver as is = %v, %v", leftOut, err)
	}
	if delivery := next(2); !delivery.Delivery || !strings.Contains(delivery.LeftOut, "Second") {
		t.Fatalf("delivery = %#v", delivery)
	}
	if _, err := svc.ResolveRecoveryGate(t.Context(), gate.IssueID, "resume", "Use A"); err == nil {
		t.Fatal("the paused attempt's recovery gate stayed open")
	}
	if len(launcher.stops) == 0 {
		t.Fatal("the paused session was not stopped")
	}
}
