package state

import (
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory/model"
)

func TestPendingChildRemovalCannotCompleteACrossEpicPhaseBlocker(t *testing.T) {
	db := openTestStateDB(t)
	t.Cleanup(func() { _ = db.Close() })
	ctx := t.Context()
	formula := nativeTracerFormula(t)
	formula.Nodes = append(formula.Nodes, model.NativeFormulaNode{Key: "implement", Kind: "phase", Workflow: &model.WorkflowStep{Key: "implement", Kind: "implementation"}})
	source, err := db.CreateFactoryEpic(ctx, "", "Source phase", "", "/source", "", formula)
	if err != nil {
		t.Fatal(err)
	}
	consumer, err := db.CreateFactoryEpic(ctx, "", "Phase consumer", "", "/consumer", "", nativeTracerFormula(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, epic := range []model.NativeEpic{source, consumer} {
		proposal, err := db.SaveFactoryProposalRevision(ctx, model.NativeProposalRevision{EpicID: epic.ID, MolID: factoryIssueID(t, db, epic.ID, "mol"), Project: epic.InitialProject, ManifestJSON: `{"nodes":[]}`, ContentHash: epic.ID})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.DecideFactoryPlanGate(ctx, epic.ID, "approve", proposal.Revision, proposal.ContentHash, ""); err != nil {
			t.Fatal(err)
		}
	}
	phase := factoryIssueID(t, db, source.ID, "phase")
	for _, title := range []string{"Finished", "Unfinished"} {
		if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Action: "create", EpicID: source.ID, ParentID: phase, Kind: "task", Title: title}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.db.Exec(`UPDATE factory_issue SET status = 'closed', outcome = 'succeeded' WHERE id = ?`, issueIDWithTitle(t, db, source.ID, "Finished")); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureFactoryDeliveryIssue(ctx, source.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Action: "create", EpicID: consumer.ID, ParentID: factoryIssueID(t, db, consumer.ID, "mol"), Kind: "task", Title: "Dependent work"}); err != nil {
		t.Fatal(err)
	}
	work := issueIDWithTitle(t, db, consumer.ID, "Dependent work")
	if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Action: "link", EpicID: consumer.ID, IssueID: work, DependsOnID: phase, DependencyType: "blocks"}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertFactoryLocalExecutionAck(ctx, "local", "/consumer", "factory-implement", "v1", "user", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Actor: "mcp", Action: "delete", EpicID: source.ID, IssueID: issueIDWithTitle(t, db, source.ID, "Unfinished")}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := db.EnsureFactoryDeliveryIssue(ctx, source.ID); err != nil {
			t.Fatal(err)
		}
		if got := issueByID(t, db, source.ID, phase); got.Status != "open" || got.Outcome != "" {
			t.Fatalf("unapproved child removal completed phase: %#v", got)
		}
		if got := issueByID(t, db, consumer.ID, work); got.DispatchState != "waiting" || len(got.Blockers) != 1 || got.Blockers[0].ID != phase {
			t.Fatalf("unapproved child removal released dependent work: %#v", got)
		}
		if _, _, err := db.ClaimFactoryImplementation(ctx, consumer.ID, work, "factory-implement/v1", time.Now()); err == nil {
			t.Fatal("claimed dependent work before source approval")
		}
	}
	gate, err := db.GetFactoryPlanGate(ctx, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.DecideFactoryPlanGate(ctx, source.ID, "approve", gate.ProposalRevision, gate.ProposalHash, ""); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureFactoryDeliveryIssue(ctx, source.ID); err != nil {
		t.Fatal(err)
	}
	if got := issueByID(t, db, consumer.ID, work); got.DispatchState != "ready" {
		t.Fatalf("approved phase completion did not release dependent work: %#v", got)
	}
	if _, _, err := db.ClaimFactoryImplementation(ctx, consumer.ID, work, "factory-implement/v1", time.Now()); err != nil {
		t.Fatal(err)
	}
}
