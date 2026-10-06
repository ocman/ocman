package factory

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/state"
)

func TestNativeGraphApprovalSurvivesRestartWithoutDuplicatingWork(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := state.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	svc := NewNativeWithPlanning(db, testProjectResolver{root: "/repo"}, &fakePlanningLauncher{})
	epic := createPouredWorkEpic(t, svc, "Fill a gap")
	proposal, err := svc.SubmitProposal(t.Context(), SubmitProposalRequest{Import: true, EpicID: epic.ID, RationaleMarkdown: "Original design", Manifest: ProposalManifest{
		EpicID: epic.ID, MolID: pouredIssueID(t, svc, epic.ID, "mol"), Project: "/repo",
		Nodes: []ManifestNode{{Key: "first", Type: "implementation", Title: "First", Requirement: "required", AcceptanceCriteria: []string{"done"}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DecidePlanGate(t.Context(), epic.ID, "approve", PlanGateDecisionRequest{ExpectedRevision: proposal.Revision, ExpectedHash: proposal.ContentHash, ImplementationModel: "provider/model"}); err != nil {
		t.Fatal(err)
	}
	issues, err := svc.ListIssues(t.Context(), epic.ID)
	if err != nil {
		t.Fatal(err)
	}
	var firstID, parentID string
	for _, issue := range issues {
		if issue.Title == "First" {
			firstID, parentID = issue.ID, issue.ParentID
		}
	}
	if firstID == "" {
		t.Fatal("initial work missing")
	}
	if err := svc.MutateGraph(t.Context(), GraphMutation{Actor: "mcp", Action: "create", EpicID: epic.ID, ParentID: parentID, Kind: "task", Title: "Gap", Description: "Test the missing case"}); err != nil {
		t.Fatal(err)
	}
	detail, err := svc.GetWorkEpic(t.Context(), epic.ID)
	if err != nil || detail.PlanGate == nil || detail.Proposal == nil {
		t.Fatalf("pending graph = %#v, %v", detail, err)
	}
	stale := *detail.PlanGate
	if detail.Proposal.RationaleMarkdown != "Original design" || len(detail.Proposal.Manifest.Nodes) != 2 {
		t.Fatalf("graph proposal = %#v", detail.Proposal)
	}
	// Human corrections to the pending graph must also invalidate the snapshot.
	if err := svc.MutateGraph(t.Context(), GraphMutation{Actor: "user", Action: "edit", EpicID: epic.ID, IssueID: firstID, Title: "First corrected", Description: "Updated description"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DecidePlanGate(t.Context(), epic.ID, "approve", PlanGateDecisionRequest{ExpectedRevision: stale.ProposalRevision, ExpectedHash: stale.ProposalHash}); err == nil {
		t.Fatal("approved an outdated graph")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = state.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	svc = NewNativeWithPlanning(db, testProjectResolver{root: "/repo"}, &fakePlanningLauncher{})
	detail, err = svc.GetWorkEpic(t.Context(), epic.ID)
	if err != nil || detail.PlanGate.Resolution != "open" {
		t.Fatalf("restarted graph = %#v, %v", detail, err)
	}
	issues, err = svc.ListIssues(t.Context(), epic.ID)
	if err != nil {
		t.Fatal(err)
	}
	var gapID string
	for _, issue := range issues {
		if issue.Title == "Gap" {
			gapID = issue.ID
		}
		if issue.Kind == "implementation" || issue.Kind == "task" {
			if issue.DispatchState == "ready" {
				t.Fatalf("work ready before approval: %#v", issue)
			}
		}
	}
	if _, _, err := db.ClaimFactoryImplementation(t.Context(), epic.ID, gapID, "factory-implement/v1", time.Now()); err == nil {
		t.Fatal("claimed unapproved work after restart")
	}
	gate, err := svc.DecidePlanGate(t.Context(), epic.ID, "approve", PlanGateDecisionRequest{ExpectedRevision: detail.PlanGate.ProposalRevision, ExpectedHash: detail.PlanGate.ProposalHash})
	if err != nil || gate.ImplementationModel != "provider/model" {
		t.Fatalf("reapproval = %#v, %v", gate, err)
	}
	after, err := svc.ListIssues(t.Context(), epic.ID)
	if err != nil || len(after) != len(issues) {
		t.Fatalf("approval duplicated graph: %d -> %d, %v", len(issues), len(after), err)
	}
	if _, _, err := db.ClaimFactoryImplementation(t.Context(), epic.ID, gapID, "factory-implement/v1", time.Now()); err != nil {
		t.Fatal(err)
	}
}
