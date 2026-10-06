package factory

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
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
	proposal, err := svc.SubmitProposal(t.Context(), SubmitProposalRequest{EpicID: epic.ID, Manifest: ProposalManifest{EpicID: epic.ID, MolID: pouredIssueID(t, svc, epic.ID, "mol"), Project: "/repo", Nodes: []ManifestNode{{Key: "implement", Type: "implementation", Requirement: "required", AcceptanceCriteria: []string{"done"}}}}})
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

func TestAmendmentModelChoiceReplacesClearsOrRetainsSavedModel(t *testing.T) {
	for _, tc := range []struct {
		name, selected, want string
		useDefault           bool
	}{
		{"replacement", "p/terra", "p/terra", false},
		{"runtime default", "", "", true},
		{"omitted choice", "", "p/sol", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, err := state.Open(statetest.Path(t))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			svc := NewNativeWithPlanning(db, testProjectResolver{root: "/repo"}, &fakePlanningLauncher{})
			epic := createPouredWorkEpic(t, svc, "Replace model")
			proposal, err := svc.SubmitProposal(t.Context(), SubmitProposalRequest{Import: true, EpicID: epic.ID, Manifest: ProposalManifest{EpicID: epic.ID, MolID: pouredIssueID(t, svc, epic.ID, "mol"), Project: "/repo", Nodes: []ManifestNode{{Key: "work", Type: "implementation", Requirement: "required", AcceptanceCriteria: []string{"done"}}}}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := svc.DecidePlanGate(t.Context(), epic.ID, "approve", PlanGateDecisionRequest{ExpectedRevision: proposal.Revision, ExpectedHash: proposal.ContentHash, ImplementationModel: "p/sol"}); err != nil {
				t.Fatal(err)
			}
			work := pouredIssueID(t, svc, epic.ID, "implementation")
			if err := svc.MutateGraph(t.Context(), GraphMutation{Actor: "mcp", Action: "edit", EpicID: epic.ID, IssueID: work, Title: "Updated work"}); err != nil {
				t.Fatal(err)
			}
			detail, err := svc.GetWorkEpic(t.Context(), epic.ID)
			if err != nil {
				t.Fatal(err)
			}
			req := PlanGateDecisionRequest{ExpectedRevision: detail.PlanGate.ProposalRevision, ExpectedHash: detail.PlanGate.ProposalHash, ImplementationModel: tc.selected, UseDefaultImplementationModel: tc.useDefault}
			bad := req
			bad.ImplementationModel, bad.UseDefaultImplementationModel = "p/sol", true
			if _, err := svc.DecidePlanGate(t.Context(), epic.ID, "approve", bad); err == nil {
				t.Fatal("accepted contradictory model choices")
			}
			gate, err := svc.DecidePlanGate(t.Context(), epic.ID, "approve", req)
			if err != nil || gate.ImplementationModel != tc.want {
				t.Fatalf("model decision = %#v, %v", gate, err)
			}
			_, attempt, err := db.ClaimFactoryImplementation(t.Context(), epic.ID, work, "factory-implement/v1", time.Now())
			if err != nil || attempt.FrozenPolicy.Model != tc.want {
				t.Fatalf("claimed model = %#v, %v", attempt.FrozenPolicy, err)
			}
		})
	}
}
