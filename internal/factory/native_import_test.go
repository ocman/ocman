package factory

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/state"
)

func TestNativeImportedPlanKeepsApprovalGate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := state.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	launcher := &fakePlanningLauncher{}
	svc := NewNativeWithPlanning(db, testProjectResolver{root: "/repo"}, launcher)
	epic := createPouredWorkEpic(t, svc, "Use existing plan")
	planID := pouredIssueID(t, svc, epic.ID, "plan")
	materializationID := pouredIssueID(t, svc, epic.ID, "materialization")
	req := SubmitProposalRequest{Import: true, EpicID: epic.ID, RationaleMarkdown: "Decisions from the original session", Manifest: ProposalManifest{
		EpicID: epic.ID, MolID: pouredIssueID(t, svc, epic.ID, "mol"), Project: "/repo",
		Nodes: []ManifestNode{
			{Key: "api", Type: "implementation", Requirement: "required", Title: "API", Description: "Implement and test the endpoint"},
			{Key: "ui", Type: "implementation", Requirement: "required", Title: "UI", Description: "Connect the form and test submission"},
		},
		Edges: []ManifestEdge{{From: "ui", To: "api", Type: "blocks"}},
	}}
	first, err := svc.SubmitProposal(t.Context(), req)
	if err != nil || first.Revision != 1 || first.ContentHash == "" {
		t.Fatalf("import = %#v, %v", first, err)
	}
	if _, err := svc.ClaimPlan(t.Context(), epic.ID, planID); err == nil {
		t.Fatal("launched a planner after import")
	}
	if _, err := svc.Materialize(t.Context(), epic.ID, materializationID); err == nil {
		t.Fatal("materialized an unapproved import")
	}
	if len(launcher.calls) != 0 {
		t.Fatalf("planning launches = %#v", launcher.calls)
	}
	initialIssues, err := svc.ListIssues(t.Context(), epic.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, issue := range initialIssues {
		if issue.ID == planID && (issue.Status != "closed" || issue.Outcome != "succeeded") {
			t.Fatalf("import did not complete planning: %#v", issue)
		}
		if issue.Kind == "implementation" || issue.Kind == "delivery" {
			t.Fatalf("import created executable work before approval: %#v", issue)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = state.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	svc = NewNativeWithPlanning(db, testProjectResolver{root: "/repo"}, launcher)
	detail, err := svc.GetWorkEpic(t.Context(), epic.ID)
	if err != nil || detail.PlanGate == nil || detail.PlanGate.Resolution != "open" || detail.PlanGate.ProposalHash != first.ContentHash {
		t.Fatalf("persisted gate = %#v, %v", detail.PlanGate, err)
	}
	attempts, err := db.ListFactoryAttempts(t.Context(), epic.ID)
	if err != nil || len(attempts) != 0 {
		t.Fatalf("attempts before approval = %#v, %v", attempts, err)
	}
	if _, err := svc.DecidePlanGate(t.Context(), epic.ID, "revise", PlanGateDecisionRequest{ExpectedRevision: first.Revision, ExpectedHash: first.ContentHash, Feedback: "Add verification detail"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DecidePlanGate(t.Context(), epic.ID, "approve", PlanGateDecisionRequest{ExpectedRevision: first.Revision, ExpectedHash: first.ContentHash}); err == nil {
		t.Fatal("approved a revision-requested import")
	}
	req.RationaleMarkdown += "; verification clarified"
	second, err := svc.SubmitProposal(t.Context(), req)
	if err != nil || second.Revision != 2 || second.ContentHash == first.ContentHash {
		t.Fatalf("reimport = %#v, %v", second, err)
	}
	if _, err := svc.DecidePlanGate(t.Context(), epic.ID, "approve", PlanGateDecisionRequest{ExpectedRevision: first.Revision, ExpectedHash: first.ContentHash}); err == nil {
		t.Fatal("approved a stale import")
	}
	gate, err := svc.DecidePlanGate(t.Context(), epic.ID, "approve", PlanGateDecisionRequest{ExpectedRevision: second.Revision, ExpectedHash: second.ContentHash, ImplementationModel: "provider/model"})
	if err != nil || gate.Resolution != "approved" || gate.ImplementationModel != "provider/model" {
		t.Fatalf("approval = %#v, %v", gate, err)
	}
	materialized, err := svc.Materialize(t.Context(), epic.ID, materializationID)
	if err != nil || len(materialized.Issues) != 2 {
		t.Fatalf("materialized = %#v, %v", materialized, err)
	}
	issues, err := svc.ListIssues(t.Context(), epic.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, issue := range issues {
		if issue.ID == planID && (issue.Status != "closed" || issue.Outcome != "succeeded") {
			t.Fatalf("plan after approval = %#v", issue)
		}
		if issue.ManifestKey == "ui" && issue.DispatchState == "ready" {
			t.Fatal("imported UI dependency was lost")
		}
	}
	if _, err := svc.SubmitProposal(t.Context(), req); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("import after approval = %v", err)
	}
}

func TestNativeImportRejectsOwnedOrInvalidPlans(t *testing.T) {
	for _, scenario := range []string{"claimed", "rejected", "credentials", "partial credentials", "wrong scope", "cycle", "no required work", "unavailable gate"} {
		t.Run(scenario, func(t *testing.T) {
			db, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			svc := NewNativeWithPlanning(db, testProjectResolver{root: "/repo"}, &fakePlanningLauncher{})
			epic := createPouredWorkEpic(t, svc, "Import constraints")
			req := SubmitProposalRequest{Import: true, EpicID: epic.ID, Manifest: ProposalManifest{EpicID: epic.ID, MolID: pouredIssueID(t, svc, epic.ID, "mol"), Project: "/repo", Nodes: []ManifestNode{{Key: "work", Type: "implementation", Requirement: "required"}}}}
			wantErr := ErrInvalidRequest
			switch scenario {
			case "claimed":
				if _, _, err := db.ClaimFactoryPlan(t.Context(), epic.ID, pouredIssueID(t, svc, epic.ID, "plan"), planningProfile, time.Now()); err != nil {
					t.Fatal(err)
				}
			case "rejected":
				proposal, err := svc.SubmitProposal(t.Context(), req)
				if err != nil {
					t.Fatal(err)
				}
				if gate, err := svc.DecidePlanGate(t.Context(), epic.ID, "reject", PlanGateDecisionRequest{ExpectedRevision: proposal.Revision, ExpectedHash: proposal.ContentHash}); err != nil || len(gate.ReviewIssueIDs) != 0 {
					t.Fatalf("reject import left running work: %#v, %v", gate, err)
				}
			case "credentials":
				req.AttemptID, req.AttemptToken = "attempt", "token"
			case "partial credentials":
				req.AttemptToken = "token"
			case "wrong scope":
				req.Manifest.Project = "/other"
			case "cycle":
				req.Manifest.Edges = []ManifestEdge{{From: "work", To: "work", Type: "blocks"}}
			case "no required work":
				req.Manifest.Nodes[0].Requirement = "optional"
			case "unavailable gate":
				if err := svc.MutateGraph(t.Context(), GraphMutation{Action: "delete", EpicID: epic.ID, IssueID: pouredIssueID(t, svc, epic.ID, "gate")}); err != nil {
					t.Fatal(err)
				}
			}
			before, err := svc.ListProposals(t.Context(), epic.ID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := svc.SubmitProposal(t.Context(), req); !errors.Is(err, wantErr) {
				t.Fatalf("import = %v, want %v", err, wantErr)
			}
			after, err := svc.ListProposals(t.Context(), epic.ID)
			if err != nil || len(after) != len(before) {
				t.Fatalf("failed import changed proposals: %#v, %v", after, err)
			}
		})
	}
}
