package state

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory/model"
)

func TestGraphAmendmentReopensOnlyTheDurablePlanGate(t *testing.T) {
	db := openTestStateDB(t)
	t.Cleanup(func() { _ = db.Close() })
	ctx := t.Context()
	epic, err := db.CreateFactoryEpicWithProjects(ctx, "", "Gate identity", "", "/repo", "", nativeTracerFormula(t), []string{"/other"})
	if err != nil {
		t.Fatal(err)
	}
	mol := factoryIssueID(t, db, epic.ID, "mol")
	proposal, err := db.SaveFactoryProposalRevision(ctx, model.NativeProposalRevision{EpicID: epic.ID, MolID: mol, Project: "/repo", ManifestJSON: `{"nodes":[]}`, ContentHash: "initial"})
	if err != nil {
		t.Fatal(err)
	}
	gate, err := db.DecideFactoryPlanGate(ctx, epic.ID, "approve", proposal.Revision, proposal.ContentHash, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Action: "create", EpicID: epic.ID, ParentID: mol, Kind: "task", Title: "Original work"}); err != nil {
		t.Fatal(err)
	}
	// A later recovery Issue sorts before .1.2 and belongs to a different project.
	recovery := mol + ".10"
	if recovery >= gate.IssueID {
		t.Fatal("fixture recovery must sort before the Plan gate")
	}
	if _, err := db.db.Exec(`INSERT INTO factory_issue(id, epic_id, project_path, kind, title, status, outcome, outcome_reason, created_at) VALUES (?, ?, '/other', 'gate', 'Recovery gate', 'closed', 'failed', 'Recovery rejected', 1)`, recovery, epic.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Actor: "mcp", Action: "edit", EpicID: epic.ID, IssueID: issueIDWithTitle(t, db, epic.ID, "Original work"), Title: "Amended work"}); err != nil {
		t.Fatal(err)
	}
	approval := issueByID(t, db, epic.ID, gate.IssueID)
	unrelated := issueByID(t, db, epic.ID, recovery)
	if approval.Status != "open" || approval.Outcome != "" || unrelated.Status != "closed" || unrelated.Outcome != "failed" || unrelated.OutcomeReason != "Recovery rejected" {
		t.Fatalf("wrong gate reopened: approval=%#v, recovery=%#v", approval, unrelated)
	}
	snapshot, err := db.GetFactoryProposalRevision(ctx, epic.ID, 0)
	if err != nil || snapshot.Project != "/repo" {
		t.Fatalf("wrong snapshot project: %#v, %v", snapshot, err)
	}
}

func TestPendingLastTaskRemovalPreservesWorkflowMergeGate(t *testing.T) {
	db := openTestStateDB(t)
	t.Cleanup(func() { _ = db.Close() })
	ctx := t.Context()
	formula := nativeTracerFormula(t)
	formula.Nodes = append(formula.Nodes,
		model.NativeFormulaNode{Key: "implement", Kind: "phase", Workflow: &model.WorkflowStep{Key: "implement", Kind: "implementation"}},
		model.NativeFormulaNode{Key: "verify", Kind: "workflow_template", Workflow: &model.WorkflowStep{Key: "verify", Kind: "verification", Needs: []string{"implement"}}},
		model.NativeFormulaNode{Key: "deliver", Kind: "workflow_template", Workflow: &model.WorkflowStep{Key: "deliver", Kind: "delivery", Needs: []string{"verify"}}},
	)
	create := func(title, project string, formula model.NativeFormula, parentKind string) (string, string) {
		epic, err := db.CreateFactoryEpic(ctx, "", title, "", project, "", formula)
		if err != nil {
			t.Fatal(err)
		}
		mol := factoryIssueID(t, db, epic.ID, "mol")
		proposal, err := db.SaveFactoryProposalRevision(ctx, model.NativeProposalRevision{EpicID: epic.ID, MolID: mol, Project: project, ManifestJSON: `{"nodes":[]}`, ContentHash: title})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.DecideFactoryPlanGate(ctx, epic.ID, "approve", proposal.Revision, proposal.ContentHash, ""); err != nil {
			t.Fatal(err)
		}
		if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Action: "create", EpicID: epic.ID, ParentID: factoryIssueID(t, db, epic.ID, parentKind), Kind: "task", Title: title + " work"}); err != nil {
			t.Fatal(err)
		}
		return epic.ID, issueIDWithTitle(t, db, epic.ID, title+" work")
	}
	source, work := create("Source", "/source", formula, "phase")
	consumerEpic, consumer := create("Consumer", "/consumer", nativeTracerFormula(t), "mol")
	if err := db.EnsureFactoryDeliveryIssue(ctx, source); err != nil {
		t.Fatal(err)
	}
	delivery := factoryIssueID(t, db, source, "delivery")
	if err := db.UpsertFactoryLocalExecutionAck(ctx, "local", "/consumer", "factory-implement", "v1", "user", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Action: "link", EpicID: consumerEpic, IssueID: consumer, DependsOnID: delivery, DependencyType: "merge_gated"}); err != nil {
		t.Fatal(err)
	}
	if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Actor: "mcp", Action: "delete", EpicID: source, IssueID: work}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := db.EnsureFactoryDeliveryIssue(ctx, source); err != nil {
			t.Fatal(err)
		}
		if got := issueByID(t, db, consumerEpic, consumer); got.DispatchState != "waiting" || len(got.Blockers) != 1 || got.Blockers[0].Type != "merge_gated" {
			t.Fatalf("pending removal released merge gate: %#v", got)
		}
		if _, _, err := db.ClaimFactoryImplementation(ctx, consumerEpic, consumer, "factory-implement/v1", time.Now()); err == nil {
			t.Fatal("claimed consumer before source removal approval")
		}
	}
	gate, err := db.GetFactoryPlanGate(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.DecideFactoryPlanGate(ctx, source, "approve", gate.ProposalRevision, gate.ProposalHash, ""); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureFactoryDeliveryIssue(ctx, source); err != nil {
		t.Fatal(err)
	}
	if got := issueByID(t, db, consumerEpic, consumer); got.DispatchState != "ready" {
		t.Fatalf("approved no-work cleanup did not release consumer: %#v", got)
	}
	if _, _, err := db.ClaimFactoryImplementation(ctx, consumerEpic, consumer, "factory-implement/v1", time.Now()); err != nil {
		t.Fatal(err)
	}
}

func TestGraphAmendmentWithoutPlanGateFailsClosed(t *testing.T) {
	db := openTestStateDB(t)
	t.Cleanup(func() { _ = db.Close() })
	tx, err := db.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := reopenFactoryGraphApprovalTx(t.Context(), tx, "missing", nil); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing Plan gate = %v", err)
	}
}

func TestHandBuiltScopeFindsRootFormulaApproval(t *testing.T) {
	db := openTestStateDB(t)
	t.Cleanup(func() { _ = db.Close() })
	epic, err := db.CreateFactoryEpic(t.Context(), "", "Hand-built scope", "", "/repo", "", nativeTracerFormula(t))
	if err != nil {
		t.Fatal(err)
	}
	mol := factoryIssueID(t, db, epic.ID, "mol")
	approval := factoryIssueID(t, db, epic.ID, "gate")
	recovery := mol + ".10"
	if _, err := db.db.Exec(`INSERT INTO factory_issue(id, epic_id, project_path, kind, title, status, created_at) VALUES (?, ?, '/repo', 'gate', 'Recovery gate', 'closed', 1)`, recovery, epic.ID); err != nil {
		t.Fatal(err)
	}
	tx, err := db.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := reopenFactoryGraphApprovalTx(t.Context(), tx, epic.ID, nil); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	gate, err := db.GetFactoryPlanGate(t.Context(), epic.ID)
	if err != nil || gate.IssueID != approval || issueByID(t, db, epic.ID, recovery).Status != "closed" {
		t.Fatalf("hand-built scope selected wrong gate: %#v, %v", gate, err)
	}
}
