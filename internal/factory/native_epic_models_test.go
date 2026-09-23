package factory

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/factory/model"
	"github.com/NoUseFreak/ocman/internal/state"
)

func TestEpicPlanModelOverridesPlanningDefault(t *testing.T) {
	db, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	planner := &fakePlanningLauncher{result: PlanningSession{Platform: "opencode", ID: "plan"}}
	svc := NewNativeWithPlanning(db, testProjectResolver{root: "/repo"}, planner)
	epic := createPouredWorkEpic(t, svc, "Plan with a chosen model")
	if _, err := svc.SetEpicModels(t.Context(), epic.ID, model.EpicModels{Plan: "bad"}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("invalid model err = %v", err)
	}
	if _, err := svc.SetEpicModels(t.Context(), "missing", model.EpicModels{}); !errors.Is(err, ErrWorkEpicNotFound) {
		t.Fatalf("missing epic err = %v", err)
	}
	if _, err := svc.SetEpicModels(t.Context(), epic.ID, model.EpicModels{Plan: " test/planner "}); err != nil {
		t.Fatal(err)
	}
	if got, err := svc.GetWorkEpic(t.Context(), epic.ID); err != nil || got.Models.Plan != "test/planner" {
		t.Fatalf("epic models = %#v, %v", got.Models, err)
	}
	if _, err := svc.ClaimPlan(t.Context(), epic.ID, pouredIssueID(t, svc, epic.ID, "plan")); err != nil {
		t.Fatal(err)
	}
	if len(planner.calls) != 1 || planner.calls[0].Model != "test/planner" {
		t.Fatalf("planning launches = %#v", planner.calls)
	}
}

func TestEpicModelsApplyToUnstartedWorkOnly(t *testing.T) {
	db, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	launcher := &fakeImplementationLauncher{store: db}
	svc := NewNativeWithExecution(db, testProjectResolver{root: "/repo"}, &fakePlanningLauncher{}, launcher)
	source := strings.Replace(tracerWorkflowSource, "  verify:\n    kind: verification", "  verify:\n    kind: verification\n    config:\n      model: test/template", 1)
	formula, err := svc.SaveFormula(t.Context(), FormulaSaveRequest{ID: "custom/models", Source: source})
	if err != nil {
		t.Fatal(err)
	}
	epic, err := svc.CreateWorkEpic(t.Context(), CreateWorkEpicRequest{Goal: "Ship", InitialProject: "/repo", AcknowledgeLocalExecution: true, FormulaID: formula.ID, FormulaRevision: formula.Version})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Pour(t.Context(), epic.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetEpicModels(t.Context(), epic.ID, model.EpicModels{Implementation: "test/first", Verification: "test/verifier"}); err != nil {
		t.Fatal(err)
	}
	proposal, err := svc.SubmitProposal(t.Context(), SubmitProposalRequest{EpicID: epic.ID, Manifest: ProposalManifest{EpicID: epic.ID, MolID: pouredIssueID(t, svc, epic.ID, "mol"), Project: "/repo", Nodes: []ManifestNode{{Key: "one", Type: "implementation", Requirement: "required"}, {Key: "two", Type: "implementation", Requirement: "required"}}}})
	if err != nil {
		t.Fatal(err)
	}
	// The epic setting wins over the approval-time choice.
	if _, err := svc.DecidePlanGate(t.Context(), epic.ID, "approve", PlanGateDecisionRequest{ExpectedRevision: proposal.Revision, ExpectedHash: proposal.ContentHash, ImplementationModel: "test/gate"}); err != nil {
		t.Fatal(err)
	}
	want := []string{"test/first", "test/second", "test/verifier"}
	for index, session := range []string{"one", "two", "verification"} {
		launcher.result = PlanningSession{Platform: "opencode", ID: session}
		if err := svc.Dispatch(t.Context()); err != nil {
			t.Fatal(err)
		}
		if len(launcher.prompts) != index+1 {
			t.Fatalf("dispatch %d = %#v", index, launcher.prompts)
		}
		request := launcher.prompts[index]
		if request.Model != want[index] {
			t.Fatalf("dispatch %d model = %q, want %q", index, request.Model, want[index])
		}
		if index == 0 {
			// Changing mid-implementation must not touch the claimed attempt.
			if _, err := svc.SetEpicModels(t.Context(), epic.ID, model.EpicModels{Implementation: "test/second", Verification: "test/verifier"}); err != nil {
				t.Fatal(err)
			}
			attempts, err := db.ListFactoryAttempts(t.Context(), epic.ID)
			if err != nil || attempts[len(attempts)-1].FrozenPolicy.Model != "test/first" {
				t.Fatalf("claimed attempt changed: %#v, %v", attempts, err)
			}
		}
		if err := svc.CompleteAttempt(t.Context(), request.AttemptID, request.AgentToken, "done", ""); err != nil {
			t.Fatal(err)
		}
	}
}
