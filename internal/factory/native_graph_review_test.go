package factory

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
)

func TestAgentCannotReplaceUnmaterializedInitialProposal(t *testing.T) {
	db, err := state.Open(statetest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	svc := NewNativeWithPlanning(db, testProjectResolver{root: "/repo"}, &fakePlanningLauncher{})
	epic := createPouredWorkEpic(t, svc, "Preserve initial tickets")
	proposal, err := svc.SubmitProposal(t.Context(), SubmitProposalRequest{Import: true, EpicID: epic.ID, Manifest: ProposalManifest{
		EpicID: epic.ID, MolID: pouredIssueID(t, svc, epic.ID, "mol"), Project: "/repo",
		Nodes: []ManifestNode{{Key: "api", Type: "implementation", Title: "API", Requirement: "required", AcceptanceCriteria: []string{"API works"}}, {Key: "ui", Type: "implementation", Title: "UI", Requirement: "required", AcceptanceCriteria: []string{"UI works"}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	issues, err := svc.ListIssues(t.Context(), epic.ID)
	if err != nil {
		t.Fatal(err)
	}
	var parent string
	for _, issue := range issues {
		if issue.Kind == "phase" {
			parent = issue.ID
		}
	}
	if parent == "" {
		parent = proposal.MolID
	}
	err = svc.MutateGraph(t.Context(), GraphMutation{Actor: "mcp", Action: "create", EpicID: epic.ID, ParentID: parent, Kind: "task", Title: "Gap"})
	if err == nil || !strings.Contains(err.Error(), "initial proposal") {
		t.Fatalf("agent replaced initial proposal: %v", err)
	}
	detail, err := svc.GetWorkEpic(t.Context(), epic.ID)
	if err != nil || detail.PlanGate.ProposalHash != proposal.ContentHash || detail.PlanGate.ProposalRevision != proposal.Revision {
		t.Fatalf("initial identity changed: %#v, %v", detail, err)
	}
	// Approval alone is not materialization; a failed materializer must stay safe.
	if _, err := db.DecideFactoryPlanGate(t.Context(), epic.ID, "approve", proposal.Revision, proposal.ContentHash, ""); err != nil {
		t.Fatal(err)
	}
	if err := svc.MutateGraph(t.Context(), GraphMutation{Actor: "mcp", Action: "create", EpicID: epic.ID, ParentID: parent, Kind: "task", Title: "Gap"}); err == nil {
		t.Fatal("agent replaced approved but unmaterialized proposal")
	}
	if _, err := svc.DecidePlanGate(t.Context(), epic.ID, "approve", PlanGateDecisionRequest{ExpectedRevision: proposal.Revision, ExpectedHash: proposal.ContentHash}); err != nil {
		t.Fatal(err)
	}
	issues, err = svc.ListIssues(t.Context(), epic.ID)
	if err != nil {
		t.Fatal(err)
	}
	titles := map[string]bool{}
	for _, issue := range issues {
		if issue.Kind == "implementation" || issue.Kind == "task" {
			titles[issue.Title] = true
		}
	}
	if !titles["API"] || !titles["UI"] || titles["Gap"] {
		t.Fatalf("initial tickets lost: %#v", titles)
	}
}

func TestProposalSubmissionCannotForgeFrozenGraphRevision(t *testing.T) {
	svc := NewNative(&nativeStoreFake{})
	baseline := 1
	for _, manifest := range []ProposalManifest{
		{Issues: []Issue{}},
		{ExternalIssues: []Issue{}},
		{BaseRevision: &baseline},
		{BaseIssues: []Issue{}},
	} {
		if _, err := svc.proposalForRequest(t.Context(), SubmitProposalRequest{Manifest: manifest}); err == nil || !strings.Contains(err.Error(), "output-only") {
			t.Fatalf("submitted output-only graph: %v", err)
		}
	}
}

func TestGraphProposalResponsePreservesHierarchyAndExternalReferences(t *testing.T) {
	db, err := state.Open(statetest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	svc := NewNativeWithPlanning(db, testProjectResolver{root: "/repo"}, &fakePlanningLauncher{})
	// The legacy graph supports editable nested Mols and cross-Epic dependencies.
	create := func(name string) string {
		formula, err := svc.nativeFormula(t.Context(), "ocman/tracer", 2)
		if err != nil {
			t.Fatal(err)
		}
		epic, err := db.CreateFactoryEpic(t.Context(), "", name, "", "/repo", "", formula)
		if err != nil {
			t.Fatal(err)
		}
		return epic.ID
	}
	local, external := create("Local"), create("External")
	mol := pouredIssueID(t, svc, local, "mol")
	for _, title := range []string{"Original parent", "New parent"} {
		if err := svc.MutateGraph(t.Context(), GraphMutation{Action: "create", EpicID: local, ParentID: mol, Kind: "mol", Title: title}); err != nil {
			t.Fatal(err)
		}
	}
	ids := func(epic string) map[string]string {
		issues, err := svc.ListIssues(t.Context(), epic)
		if err != nil {
			t.Fatal(err)
		}
		result := map[string]string{}
		for _, issue := range issues {
			result[issue.Title] = issue.ID
		}
		return result
	}
	parents := ids(local)
	if err := svc.MutateGraph(t.Context(), GraphMutation{Action: "create", EpicID: local, ParentID: parents["Original parent"], Kind: "task", Title: "Work"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.MutateGraph(t.Context(), GraphMutation{Action: "create", EpicID: external, ParentID: pouredIssueID(t, svc, external, "mol"), Kind: "task", Title: "External blocker"}); err != nil {
		t.Fatal(err)
	}
	work, blocker := ids(local)["Work"], ids(external)["External blocker"]
	for _, epicID := range []string{local, external} {
		proposal, err := svc.SubmitProposal(t.Context(), SubmitProposalRequest{Import: true, EpicID: epicID, Manifest: ProposalManifest{EpicID: epicID, MolID: pouredIssueID(t, svc, epicID, "mol"), Project: "/repo", Nodes: []ManifestNode{{Key: "work", Type: "implementation", Requirement: "required", AcceptanceCriteria: []string{"done"}}}}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.DecidePlanGate(t.Context(), epicID, "approve", PlanGateDecisionRequest{ExpectedRevision: proposal.Revision, ExpectedHash: proposal.ContentHash}); err != nil {
			t.Fatal(err)
		}
	}
	for _, mutation := range []GraphMutation{
		{Action: "reparent", IssueID: work, ParentID: parents["New parent"]},
		{Action: "link", IssueID: work, DependsOnID: blocker, DependencyType: "blocks"},
	} {
		mutation.EpicID, mutation.Actor = local, "mcp"
		if err := svc.MutateGraph(t.Context(), mutation); err != nil {
			t.Fatal(err)
		}
	}
	assertResponse := func(linked bool) {
		proposal, err := svc.GetProposal(t.Context(), local, 0)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(proposal)
		if err != nil {
			t.Fatal(err)
		}
		var wire struct {
			Manifest struct {
				Issues []map[string]any `json:"issues"`
			} `json:"manifest"`
		}
		if err := json.Unmarshal(encoded, &wire); err != nil {
			t.Fatal(err)
		}
		if len(wire.Manifest.Issues) == 0 || wire.Manifest.Issues[0]["id"] == nil || wire.Manifest.Issues[0]["title"] == nil {
			t.Fatalf("snapshot does not use browser issue fields: %s", encoded)
		}
		var response struct {
			Manifest struct {
				Issues         []Issue
				ExternalIssues []Issue
				BaseIssues     []Issue
			}
		}
		if err := json.Unmarshal(encoded, &response); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, issue := range response.Manifest.Issues {
			if issue.ID == work {
				found = true
				if issue.ParentID != parents["New parent"] || (len(issue.DependsOn) != 0) != linked {
					t.Fatalf("frozen structure lost: %#v", issue)
				}
			}
		}
		if !found || (len(response.Manifest.ExternalIssues) != 0) != linked {
			t.Fatalf("response lost frozen graph: %s", encoded)
		}
		baselineFound := false
		for _, issue := range response.Manifest.BaseIssues {
			if issue.ID == work {
				baselineFound = true
				if issue.ParentID != parents["Original parent"] {
					t.Fatalf("materialized baseline changed after amendment: %#v", issue)
				}
			}
		}
		if !baselineFound {
			t.Fatal("first amendment lost the materialized baseline")
		}
		if linked && (response.Manifest.ExternalIssues[0].ID != blocker || response.Manifest.ExternalIssues[0].EpicID != external || response.Manifest.ExternalIssues[0].Title != "External blocker") {
			t.Fatalf("external reference lost: %#v", response.Manifest.ExternalIssues)
		}
	}
	assertResponse(true)
	if err := svc.MutateGraph(t.Context(), GraphMutation{Actor: "mcp", Action: "unlink", EpicID: local, IssueID: work, DependsOnID: blocker, DependencyType: "blocks"}); err != nil {
		t.Fatal(err)
	}
	assertResponse(false)
}
