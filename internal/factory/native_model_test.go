package factory

import (
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/state"
)

func TestApprovalFreezesImplementationModel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := state.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	launcher := &fakeImplementationLauncher{}
	svc := NewNativeWithExecution(db, testProjectResolver{root: "/repo"}, &fakePlanningLauncher{}, launcher)
	epic := createPouredWorkEpic(t, svc, "Choose implementation model")
	proposal, err := svc.SubmitProposal(t.Context(), SubmitProposalRequest{EpicID: epic.ID, Manifest: ProposalManifest{EpicID: epic.ID, MolID: pouredIssueID(t, svc, epic.ID, "mol"), Project: "/repo", Nodes: []ManifestNode{{Key: "implement", Type: "implementation", Requirement: "required"}}}})
	if err != nil {
		t.Fatal(err)
	}
	req := PlanGateDecisionRequest{ExpectedRevision: proposal.Revision, ExpectedHash: proposal.ContentHash, ImplementationModel: "openai/gpt-5.6-sol"}
	for _, invalid := range []string{"sol", "/sol", "openai/", "openai/sol\nwrong"} {
		bad := req
		bad.ImplementationModel = invalid
		if _, err := svc.DecidePlanGate(t.Context(), epic.ID, "approve", bad); err == nil {
			t.Fatalf("accepted %q", invalid)
		}
	}
	gate, err := svc.DecidePlanGate(t.Context(), epic.ID, "approve", req)
	if err != nil || gate.ImplementationModel != req.ImplementationModel {
		t.Fatalf("gate = %#v, %v", gate, err)
	}
	if len(launcher.calls) != 1 || launcher.calls[0].Model != req.ImplementationModel {
		t.Fatalf("launches = %#v", launcher.calls)
	}
	saved, err := db.GetFactoryPlanGate(t.Context(), epic.ID)
	if err != nil || saved.ImplementationModel != req.ImplementationModel {
		t.Fatalf("saved gate = %#v, %v", saved, err)
	}
	attempts, err := db.ListFactoryAttempts(t.Context(), epic.ID)
	if err != nil || len(attempts) != 1 || attempts[0].FrozenPolicy.Model != req.ImplementationModel {
		t.Fatalf("attempts = %#v, %v", attempts, err)
	}
	req.ImplementationModel = "anthropic/claude-sonnet-4"
	repeated, err := svc.DecidePlanGate(t.Context(), epic.ID, "approve", req)
	if err != nil || repeated.ImplementationModel != gate.ImplementationModel || len(launcher.calls) != 1 {
		t.Fatalf("repeated = %#v, %v", repeated, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = state.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	saved, err = db.GetFactoryPlanGate(t.Context(), epic.ID)
	if err != nil || saved.ImplementationModel != gate.ImplementationModel {
		t.Fatalf("recovered gate = %#v, %v", saved, err)
	}
}
