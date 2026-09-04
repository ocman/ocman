package factory

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/factory/model"
	"github.com/NoUseFreak/ocman/internal/state"
)

func TestNativeFactoryFlowPersistsAndDispatchesOnlyApprovedWork(t *testing.T) {
	t.Run("approved proposal survives restart and dispatches", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "state.db")
		db, err := state.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		planning := &fakePlanningLauncher{result: PlanningSession{Platform: "opencode", ID: "plan-1"}}
		implementation := &fakeImplementationLauncher{}
		svc := NewNativeWithExecution(db, testProjectResolver{root: "/repo"}, planning, implementation)

		epic := createPouredWorkEpic(t, svc, "Ship Factory")
		planID := pouredIssueID(t, svc, epic.ID, "plan")
		if _, err := svc.ClaimPlan(context.Background(), epic.ID, planID); err != nil {
			t.Fatal(err)
		}
		if len(planning.calls) != 1 || planning.calls[0].WorkID != planID {
			t.Fatalf("planning launches = %#v", planning.calls)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}

		db, err = state.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		svc = NewNativeWithExecution(db, testProjectResolver{root: "/repo"}, planning, implementation)
		manifest := ProposalManifest{EpicID: epic.ID, MolID: pouredIssueID(t, svc, epic.ID, "mol"), Project: "/repo", Nodes: []ManifestNode{{Key: "implement", Type: "implementation", Requirement: "required"}}}
		proposal, err := svc.SubmitProposal(context.Background(), SubmitProposalRequest{EpicID: epic.ID, Manifest: manifest})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.DecidePlanGate(context.Background(), epic.ID, "approve", PlanGateDecisionRequest{ExpectedRevision: proposal.Revision, ExpectedHash: "stale"}); err == nil {
			t.Fatal("approved proposal with stale hash")
		}
		if _, err := svc.DecidePlanGate(context.Background(), epic.ID, "approve", PlanGateDecisionRequest{ExpectedRevision: proposal.Revision, ExpectedHash: proposal.ContentHash}); err != nil {
			t.Fatal(err)
		}
		materialization, err := svc.Materialize(context.Background(), epic.ID, pouredIssueID(t, svc, epic.ID, "materialization"))
		if err != nil {
			t.Fatal(err)
		}
		if materialization.ImplementationID == "" || len(implementation.calls) != 1 || implementation.calls[0].WorkID != materialization.ImplementationID {
			t.Fatalf("materialization = %#v, implementation launches = %#v", materialization, implementation.calls)
		}
		attempts, err := db.ListFactoryAttempts(context.Background(), epic.ID)
		if err != nil || len(attempts) != 2 || attempts[0].Session.ID != "plan-1" || attempts[1].Phase != model.FactoryAttemptActive || attempts[1].Session.ID != "implementation-1" {
			t.Fatalf("persisted attempts = %#v, %v", attempts, err)
		}
	})

	t.Run("rejected proposal creates no implementation", func(t *testing.T) {
		db, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		implementation := &fakeImplementationLauncher{}
		svc := NewNativeWithExecution(db, testProjectResolver{root: "/repo"}, &fakePlanningLauncher{}, implementation)
		epic := createPouredWorkEpic(t, svc, "Reject Factory")
		proposal, err := svc.SubmitProposal(context.Background(), SubmitProposalRequest{EpicID: epic.ID, Manifest: ProposalManifest{EpicID: epic.ID, MolID: pouredIssueID(t, svc, epic.ID, "mol"), Project: "/repo", Nodes: []ManifestNode{{Key: "implement", Type: "implementation", Requirement: "required"}}}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.DecidePlanGate(context.Background(), epic.ID, "reject", PlanGateDecisionRequest{ExpectedRevision: proposal.Revision, ExpectedHash: proposal.ContentHash}); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.Materialize(context.Background(), epic.ID, pouredIssueID(t, svc, epic.ID, "materialization")); err == nil {
			t.Fatal("materialized rejected proposal")
		}
		issues, err := svc.ListIssues(context.Background(), epic.ID)
		if err != nil {
			t.Fatal(err)
		}
		if hasManifestKey(issues, "implement") || len(implementation.calls) != 0 {
			t.Fatalf("rejected issues = %#v, implementation launches = %#v", issues, implementation.calls)
		}
	})
}
