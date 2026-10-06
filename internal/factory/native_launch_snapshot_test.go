package factory

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory/model"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
)

type launchInterleavingStore struct {
	*state.DB
	beforeClaim func()
	afterClaim  func(*model.FactoryAttempt)
}

func (s *launchInterleavingStore) ClaimFactoryImplementation(ctx context.Context, epic, work, profile string, at time.Time) (model.NativeEpic, model.FactoryAttempt, error) {
	if s.beforeClaim != nil {
		callback := s.beforeClaim
		s.beforeClaim = nil
		callback()
	}
	claimedEpic, attempt, err := s.DB.ClaimFactoryImplementation(ctx, epic, work, profile, at)
	if err == nil && s.afterClaim != nil {
		s.afterClaim(&attempt)
	}
	return claimedEpic, attempt, err
}

func TestDispatchFailsUnusableClaimSnapshot(t *testing.T) {
	for _, missing := range []bool{true, false} {
		t.Run(map[bool]string{true: "missing issue", false: "corrupt ancestry"}[missing], func(t *testing.T) {
			db, err := state.Open(statetest.Path(t))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			planner := NewNativeWithPlanning(db, testProjectResolver{root: "/repo"}, &fakePlanningLauncher{})
			epic := createPouredWorkEpic(t, planner, "Unusable claim")
			proposal, err := planner.SubmitProposal(t.Context(), SubmitProposalRequest{Import: true, EpicID: epic.ID, Manifest: ProposalManifest{EpicID: epic.ID, MolID: pouredIssueID(t, planner, epic.ID, "mol"), Project: "/repo", Nodes: []ManifestNode{{Key: "work", Type: "implementation", Requirement: "required", AcceptanceCriteria: []string{"done"}}}}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := planner.DecidePlanGate(t.Context(), epic.ID, "approve", PlanGateDecisionRequest{ExpectedRevision: proposal.Revision, ExpectedHash: proposal.ContentHash}); err != nil {
				t.Fatal(err)
			}
			store := &launchInterleavingStore{DB: db, afterClaim: func(attempt *model.FactoryAttempt) {
				if missing {
					attempt.LaunchIssues = nil
				} else {
					attempt.LaunchIssues = []model.NativeIssue{{ID: attempt.WorkID, ParentID: "missing"}}
				}
			}}
			launcher := &fakeImplementationLauncher{store: db}
			dispatcher := NewNativeWithExecution(store, testProjectResolver{root: "/repo"}, &fakePlanningLauncher{}, launcher)
			if err := dispatcher.Dispatch(t.Context()); err != nil {
				t.Fatal(err)
			}
			attempts, err := db.ListFactoryAttempts(t.Context(), epic.ID)
			if err != nil || len(attempts) != 1 || attempts[0].Phase != model.FactoryAttemptTerminal || attempts[0].Failure.Type != "launch_failed" || len(launcher.prompts) != 0 {
				t.Fatalf("unusable context launched: %#v, %v", attempts, err)
			}
		})
	}
}

func TestClaimSnapshotResolvesAncestryAndCriteriaWithoutLiveReads(t *testing.T) {
	db, err := state.Open(statetest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	svc := NewNative(db)
	formula, err := svc.SaveFormula(t.Context(), FormulaSaveRequest{ID: "custom/claim", Source: "prompt_implementation = \"Claimed ancestry\"\n" + tracerFormulaSource})
	if err != nil {
		t.Fatal(err)
	}
	svc.store = promptIssueStore{DB: db, issues: []model.NativeIssue{{ID: "work", Description: "Later requirements"}}}
	snapshot := []model.NativeIssue{{ID: "work", Kind: "implementation", ParentID: "mol", Project: "/repo", Description: "Claimed requirements"}, {ID: "mol", FormulaID: formula.ID, FormulaVersion: formula.Version, FormulaHash: formula.Hash}}
	prompt, err := svc.issuePromptFromIssues(t.Context(), model.NativeEpic{}, "work", "implementation", snapshot)
	if err != nil || prompt != "Claimed ancestry" {
		t.Fatalf("prompt = %q, %v", prompt, err)
	}
	criteria := verificationCriteriaFromIssues(snapshot, "/repo")
	if !strings.Contains(criteria, "Claimed requirements") || strings.Contains(criteria, "Later requirements") {
		t.Fatalf("criteria = %q", criteria)
	}
}

func TestDispatchLaunchesInputsFromTheClaimedRevision(t *testing.T) {
	db, err := state.Open(statetest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	planner := NewNativeWithPlanning(db, testProjectResolver{root: "/repo"}, &fakePlanningLauncher{})
	epic := createPouredWorkEpic(t, planner, "Consistent launch")
	proposal, err := planner.SubmitProposal(t.Context(), SubmitProposalRequest{Import: true, EpicID: epic.ID, Manifest: ProposalManifest{EpicID: epic.ID, MolID: pouredIssueID(t, planner, epic.ID, "mol"), Project: "/repo", Nodes: []ManifestNode{{Key: "work", Type: "implementation", Requirement: "required", Title: "Original title", Description: "Original requirements", AcceptanceCriteria: []string{"done"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := planner.DecidePlanGate(t.Context(), epic.ID, "approve", PlanGateDecisionRequest{ExpectedRevision: proposal.Revision, ExpectedHash: proposal.ContentHash}); err != nil {
		t.Fatal(err)
	}
	work := pouredIssueID(t, planner, epic.ID, "implementation")
	store := &launchInterleavingStore{DB: db}
	var approved PlanGate
	store.beforeClaim = func() {
		if err := planner.MutateGraph(t.Context(), GraphMutation{Actor: "mcp", Action: "edit", EpicID: epic.ID, IssueID: work, Title: "Approved title", Description: "Approved requirements"}); err != nil {
			t.Fatal(err)
		}
		gate, err := db.GetFactoryPlanGate(t.Context(), epic.ID)
		if err != nil {
			t.Fatal(err)
		}
		approved, err = planner.DecidePlanGate(t.Context(), epic.ID, "approve", PlanGateDecisionRequest{ExpectedRevision: gate.ProposalRevision, ExpectedHash: gate.ProposalHash})
		if err != nil {
			t.Fatal(err)
		}
	}
	launcher := &fakeImplementationLauncher{store: db}
	dispatcher := NewNativeWithExecution(store, testProjectResolver{root: "/repo"}, &fakePlanningLauncher{}, launcher)
	if err := dispatcher.Dispatch(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(launcher.prompts) != 1 {
		t.Fatalf("launches = %#v", launcher.prompts)
	}
	request := launcher.prompts[0]
	if request.Title != "Approved title" || request.Description != "Approved requirements" {
		t.Fatalf("launched stale inputs: %#v", request)
	}
	attempt, found, err := db.GetFactoryAttempt(t.Context(), request.AttemptID)
	if err != nil || !found || attempt.FrozenPolicy.PlanRevision != approved.ProposalRevision || attempt.FrozenPolicy.PlanHash != approved.ProposalHash {
		t.Fatalf("claim differs from launched revision: %#v, %v", attempt, err)
	}
}
