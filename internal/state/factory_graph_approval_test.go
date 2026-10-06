package state

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory/model"
)

func TestAgentGraphMutationRequiresFreshApproval(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	ctx := context.Background()
	epic, err := db.CreateFactoryEpic(ctx, "", "Epic", "", "/repo", "", nativeTracerFormula(t))
	if err != nil {
		t.Fatal(err)
	}
	mol := factoryIssueID(t, db, epic.ID, "mol")
	if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Action: "create", EpicID: epic.ID, ParentID: mol, Kind: "task", Title: "Existing work"}); err != nil {
		t.Fatal(err)
	}
	proposal, err := db.SaveFactoryProposalRevision(ctx, model.NativeProposalRevision{EpicID: epic.ID, MolID: mol, Project: "/repo", ManifestJSON: `{"nodes":[]}`, ContentHash: "initial"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.DecideFactoryPlanGate(ctx, epic.ID, "approve", proposal.Revision, proposal.ContentHash, "", "provider/model"); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertFactoryLocalExecutionAck(ctx, "local", "/repo", "factory-implement", "v1", "user", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Action: "create", Actor: "mcp", EpicID: epic.ID, ParentID: mol, Kind: "task", Title: "Discovered gap"}); err != nil {
		t.Fatal(err)
	}
	gate, err := db.GetFactoryPlanGate(ctx, epic.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gate.Resolution != "open" || gate.ProposalRevision <= proposal.Revision || gate.ProposalHash == proposal.ContentHash {
		t.Fatalf("mutation retained old approval: %#v", gate)
	}
	if gate.ImplementationModel != "provider/model" {
		t.Fatalf("lost model: %#v", gate)
	}
	issue := issueByID(t, db, epic.ID, issueIDWithTitle(t, db, epic.ID, "Discovered gap"))
	if issue.DispatchState == "ready" {
		t.Fatal("unapproved task is ready")
	}
	if _, _, err := db.ClaimFactoryImplementation(ctx, epic.ID, issue.ID, "factory-implement/v1", time.Now()); err == nil {
		t.Fatal("claimed unapproved task")
	}
	if _, err := db.DecideFactoryPlanGate(ctx, epic.ID, "approve", proposal.Revision, proposal.ContentHash, ""); err == nil {
		t.Fatal("old approval accepted")
	}
	if _, err := db.DecideFactoryPlanGate(ctx, epic.ID, "approve", gate.ProposalRevision, gate.ProposalHash, "", "provider/model"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.ClaimFactoryImplementation(ctx, epic.ID, issue.ID, "factory-implement/v1", time.Now()); err != nil {
		t.Fatal(err)
	}
}

func TestEveryAgentStructuralEditInvalidatesApproval(t *testing.T) {
	for _, action := range []string{"create", "edit", "reparent", "link", "unlink", "delete"} {
		t.Run(action, func(t *testing.T) {
			db := openTestStateDB(t)
			defer db.Close()
			ctx := t.Context()
			epic, err := db.CreateFactoryEpic(ctx, "", "Epic", "", "/repo", "", nativeTracerFormula(t))
			if err != nil {
				t.Fatal(err)
			}
			mol := factoryIssueID(t, db, epic.ID, "mol")
			for _, title := range []string{"First", "Second"} {
				if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Action: "create", EpicID: epic.ID, ParentID: mol, Kind: "implementation", Title: title}); err != nil {
					t.Fatal(err)
				}
			}
			issues, err := db.ListFactoryIssues(ctx, epic.ID)
			if err != nil {
				t.Fatal(err)
			}
			var first, second string
			for _, issue := range issues {
				if issue.Title == "First" {
					first = issue.ID
				}
				if issue.Title == "Second" {
					second = issue.ID
				}
			}
			if action == "unlink" {
				if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Action: "link", EpicID: epic.ID, IssueID: first, DependsOnID: second, DependencyType: "blocks"}); err != nil {
					t.Fatal(err)
				}
			}
			proposal, err := db.SaveFactoryProposalRevision(ctx, model.NativeProposalRevision{EpicID: epic.ID, MolID: mol, Project: "/repo", ManifestJSON: `{"nodes":[]}`, ContentHash: "old"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.DecideFactoryPlanGate(ctx, epic.ID, "approve", proposal.Revision, proposal.ContentHash, ""); err != nil {
				t.Fatal(err)
			}
			if err := db.EnsureFactoryDeliveryIssue(ctx, epic.ID); err != nil {
				t.Fatal(err)
			}
			mutation := model.GraphMutation{Action: action, Actor: "mcp", EpicID: epic.ID, IssueID: first, ParentID: mol, DependsOnID: second, DependencyType: "blocks", Kind: "task", Title: "Updated", Description: "Verify the gap"}
			if err := db.MutateFactoryGraph(ctx, mutation); err != nil {
				t.Fatal(err)
			}
			gate, err := db.GetFactoryPlanGate(ctx, epic.ID)
			if err != nil || gate.Resolution != "open" || gate.ProposalRevision != proposal.Revision+1 {
				t.Fatalf("gate = %#v, %v", gate, err)
			}
			saved, err := db.GetFactoryProposalRevision(ctx, epic.ID, gate.ProposalRevision)
			if err != nil {
				t.Fatal(err)
			}
			var snapshot struct {
				Issues []model.NativeIssue
				Nodes  []struct{ Key string }
				Edges  []struct{ From, To string }
			}
			if err := json.Unmarshal([]byte(saved.ManifestJSON), &snapshot); err != nil {
				t.Fatal(err)
			}
			found := false
			for _, issue := range snapshot.Issues {
				if issue.ID == first {
					found = true
				}
			}
			if found == (action == "delete") {
				t.Fatalf("snapshot does not reflect %s: %s", action, saved.ManifestJSON)
			}
			if action == "link" && len(snapshot.Edges) == 0 {
				t.Fatal("snapshot lost dependency")
			}
			// A failed edit rolls back both graph and approval identity.
			mutation.Action, mutation.Title = "edit", ""
			if err := db.MutateFactoryGraph(ctx, mutation); err == nil {
				t.Fatal("invalid edit accepted")
			}
			after, err := db.GetFactoryPlanGate(ctx, epic.ID)
			if err != nil || after.ProposalRevision != gate.ProposalRevision || after.ProposalHash != gate.ProposalHash {
				t.Fatalf("invalid edit changed gate: %#v, %v", after, err)
			}
			if _, err := db.DecideFactoryPlanGate(ctx, epic.ID, "revise", gate.ProposalRevision, gate.ProposalHash, "Clarify"); err != nil {
				t.Fatal(err)
			}
			if _, err := db.DecideFactoryPlanGate(ctx, epic.ID, "approve", gate.ProposalRevision, gate.ProposalHash, ""); err == nil {
				t.Fatal("approved revision-requested graph")
			}
		})
	}
}
