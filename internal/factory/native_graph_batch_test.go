package factory

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/state"
)

func TestGraphBatchCreatesOneReviewableRevision(t *testing.T) {
	db, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	svc := NewNativeWithPlanning(db, testProjectResolver{root: "/repo"}, &fakePlanningLauncher{})
	epic := createPouredWorkEpic(t, svc, "Batch amendments")
	proposal, err := svc.SubmitProposal(t.Context(), SubmitProposalRequest{Import: true, EpicID: epic.ID, Manifest: ProposalManifest{
		EpicID: epic.ID, MolID: pouredIssueID(t, svc, epic.ID, "mol"), Project: "/repo",
		Nodes: []ManifestNode{{Key: "first", Type: "implementation", Title: "First", Requirement: "required", AcceptanceCriteria: []string{"done"}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DecidePlanGate(t.Context(), epic.ID, "approve", PlanGateDecisionRequest{ExpectedRevision: proposal.Revision, ExpectedHash: proposal.ContentHash}); err != nil {
		t.Fatal(err)
	}
	issues, err := svc.ListIssues(t.Context(), epic.ID)
	if err != nil {
		t.Fatal(err)
	}
	var parent string
	for _, issue := range issues {
		if issue.Title == "First" {
			parent = issue.ParentID
		}
	}
	decode := func(children []map[string]any) GraphMutation {
		encoded, err := json.Marshal(map[string]any{"action": "batch", "epicId": epic.ID, "actor": "mcp", "rationaleMarkdown": "## Changes\n- Add regression coverage.\n\n## Why\nThe validator found missing cases.", "mutations": children})
		if err != nil {
			t.Fatal(err)
		}
		var mutation GraphMutation
		if err := json.Unmarshal(encoded, &mutation); err != nil {
			t.Fatal(err)
		}
		return mutation
	}
	create := map[string]any{"action": "create", "parentId": parent, "kind": "task", "title": "Regression coverage"}
	if err := svc.MutateGraph(t.Context(), decode([]map[string]any{create, {"action": "delete", "issueId": "missing"}})); err == nil {
		t.Fatal("invalid batch succeeded")
	}
	unchanged, err := svc.GetWorkEpic(t.Context(), epic.ID)
	if err != nil || unchanged.Proposal.Revision != proposal.Revision {
		t.Fatalf("failed batch changed proposal: %#v, %v", unchanged, err)
	}
	if err := svc.MutateGraph(t.Context(), decode([]map[string]any{create, {"action": "create", "parentId": parent, "kind": "task", "title": "Second case"}})); err != nil {
		t.Fatal(err)
	}
	detail, err := svc.GetWorkEpic(t.Context(), epic.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Proposal.Revision != proposal.Revision+1 || len(detail.Proposal.Manifest.Nodes) != 3 || detail.Proposal.RationaleMarkdown != "## Changes\n- Add regression coverage.\n\n## Why\nThe validator found missing cases." || detail.PlanGate.Resolution != "open" {
		t.Fatalf("batch proposal = %#v", detail)
	}
	for _, mutation := range []GraphMutation{
		{Action: "batch", EpicID: epic.ID},
		{Action: "batch", EpicID: epic.ID, Mutations: []GraphMutation{{Action: "create"}}},
		decode([]map[string]any{{"action": "batch"}}),
		decode([]map[string]any{{"action": "approve_step"}}),
		decode([]map[string]any{{"action": "create", "epicId": "other"}}),
		decode([]map[string]any{{"action": "create", "project": "/unadmitted"}}),
	} {
		if err := svc.MutateGraph(t.Context(), mutation); err == nil {
			t.Fatalf("invalid mutation succeeded: %#v", mutation)
		}
	}
	latest, err := svc.GetWorkEpic(t.Context(), epic.ID)
	if err != nil || latest.Proposal.ContentHash != detail.Proposal.ContentHash {
		t.Fatalf("invalid batch changed revision: %#v, %v", latest, err)
	}
}
