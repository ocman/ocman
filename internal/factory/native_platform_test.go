package factory

import (
	"testing"

	"github.com/NoUseFreak/ocman/internal/state"
)

func TestEpicPlatformIsPersistedAndFrozen(t *testing.T) {
	db, err := state.Open(t.TempDir() + "/state.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.SetSetting(t.Context(), state.DefaultPlatformSetting, "fake-provider"); err != nil {
		t.Fatal(err)
	}
	implementation := &fakeImplementationLauncher{}
	planning := &fakePlanningLauncher{result: PlanningSession{Platform: "fake-provider", ID: "plan"}}
	svc := NewNativeWithExecution(db, testProjectResolver{root: "/repo"}, planning, implementation)
	epic := createPouredWorkEpic(t, svc, "Platform independent of model")
	if epic.Platform != "fake-provider" {
		t.Fatalf("epic = %+v", epic)
	}
	if err := db.SetSetting(t.Context(), state.DefaultPlatformSetting, "opencode"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ClaimPlan(t.Context(), epic.ID, pouredIssueID(t, svc, epic.ID, "plan")); err != nil {
		t.Fatal(err)
	}
	if len(planning.calls) != 1 || planning.calls[0].Platform != "fake-provider" {
		t.Fatalf("planning = %+v", planning.calls)
	}
	proposal, err := svc.SubmitProposal(t.Context(), SubmitProposalRequest{EpicID: epic.ID, Manifest: ProposalManifest{EpicID: epic.ID, MolID: pouredIssueID(t, svc, epic.ID, "mol"), Project: "/repo", Nodes: []ManifestNode{{Key: "implement", Type: "implementation", Requirement: "required", AcceptanceCriteria: []string{"done"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DecidePlanGate(t.Context(), epic.ID, "approve", PlanGateDecisionRequest{ExpectedRevision: proposal.Revision, ExpectedHash: proposal.ContentHash, ImplementationModel: "openai/test"}); err != nil {
		t.Fatal(err)
	}
	if len(implementation.calls) != 1 || implementation.calls[0].Platform != "fake-provider" {
		t.Fatalf("implementation = %+v", implementation.calls)
	}
	saved, err := svc.GetWorkEpic(t.Context(), epic.ID)
	if err != nil || saved.Platform != "fake-provider" {
		t.Fatalf("saved = %+v, %v", saved, err)
	}
	other, err := svc.CreateWorkEpic(t.Context(), CreateWorkEpicRequest{Goal: "Explicit platform", InitialProject: "/repo", Platform: "another-provider", AcknowledgeLocalExecution: true})
	if err != nil || other.Platform != "another-provider" {
		t.Fatalf("explicit = %+v, %v", other, err)
	}
}
