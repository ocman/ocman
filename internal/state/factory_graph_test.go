package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory"
	"github.com/NoUseFreak/ocman/internal/factory/model"
)

func TestFactoryGraphCreateAndPourAreDurableAndAtomic(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	formula := nativeTracerFormula(t)
	epic, err := db.CreateFactoryEpic(context.Background(), "Ship search", "Keep it small", "/repo", "", formula)
	if err != nil {
		t.Fatal(err)
	}
	if _, issues, err := db.PourFactoryEpic(context.Background(), epic.ID, formula); err != nil || len(issues) != 4 {
		t.Fatal(err)
	}
	got, err := db.GetFactoryEpic(context.Background(), epic.ID)
	if err != nil || got.FormulaID != "ocman/tracer" || got.FormulaVersion != 1 {
		t.Fatalf("GetFactoryEpic = %#v, %v", got, err)
	}
	issues, err := db.ListFactoryIssues(context.Background(), epic.ID)
	if err != nil || len(issues) != 4 || issues[0].Kind != "mol" || issues[1].Kind != "plan" || issues[1].ParentID != issues[0].ID || issues[2].ParentID != issues[0].ID || issues[3].ParentID != issues[0].ID {
		t.Fatalf("ListFactoryIssues = %#v, %v", issues, err)
	}
	for table, want := range map[string]int{"factory_project": 1, "factory_epic": 1, "factory_formula_identity": 1, "factory_issue": 4, "factory_issue_hierarchy": 3, "factory_issue_dependency": 2} {
		var count int
		if err := db.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&count); err != nil || count != want {
			t.Fatalf("%s count = %d, %v, want %d", table, count, err, want)
		}
	}
}

func TestFactoryIssueCommentsAreAppendOnly(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	epic, err := db.CreateFactoryEpic(t.Context(), "Ship", "", "/repo", "", nativeTracerFormula(t))
	if err != nil {
		t.Fatal(err)
	}
	issueID := factoryIssueID(t, db, epic.ID, "plan")
	at := time.UnixMilli(1000)
	first, err := db.AppendFactoryIssueComment(t.Context(), epic.ID, issueID, "user", " First ", at)
	if err != nil || first.Body != "First" {
		t.Fatalf("first comment = %#v, %v", first, err)
	}
	if _, err := db.AppendFactoryIssueComment(t.Context(), epic.ID, issueID, "mcp", "Second", at); err != nil {
		t.Fatal(err)
	}
	comments, err := db.ListFactoryIssueComments(t.Context(), epic.ID, issueID)
	if err != nil || len(comments) != 2 || comments[0].Body != "First" || comments[1].Body != "Second" {
		t.Fatalf("comments = %#v, %v", comments, err)
	}
	if _, err := db.db.Exec(`UPDATE factory_issue_comment SET body = 'changed' WHERE id = ?`, first.ID); err == nil {
		t.Fatal("updated append-only comment")
	}
	if _, err := db.db.Exec(`DELETE FROM factory_issue_comment WHERE id = ?`, first.ID); err == nil {
		t.Fatal("deleted append-only comment")
	}
	if _, err := db.AppendFactoryIssueComment(t.Context(), epic.ID, issueID, "user", "  ", at); err == nil {
		t.Fatal("appended empty comment")
	}
	if _, err := db.AppendFactoryIssueComment(t.Context(), epic.ID, issueID, "user", strings.Repeat("x", 16001), at); err == nil {
		t.Fatal("appended oversized comment")
	}
}

func TestFactoryGraphMutationsRejectCyclesAndSoftDelete(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	ctx := context.Background()
	first, err := db.CreateFactoryEpic(ctx, "First", "", "/repo", "", nativeTracerFormula(t))
	if err != nil {
		t.Fatal(err)
	}
	second, err := db.CreateFactoryEpic(ctx, "Second", "", "/repo", "", nativeTracerFormula(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, epic := range []model.NativeEpic{first, second} {
		if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Action: "create", EpicID: epic.ID, ParentID: factoryIssueID(t, db, epic.ID, "mol"), Kind: "implementation", Title: "Work"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Action: "create", EpicID: first.ID, ParentID: factoryIssueID(t, db, first.ID, "mol"), Kind: "materialization", Title: "Duplicate dispatch"}); err == nil {
		t.Fatal("manual materialization was accepted")
	}
	firstIssues, err := db.ListFactoryIssues(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	secondIssues, err := db.ListFactoryIssues(ctx, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	firstID, secondID := firstIssues[len(firstIssues)-1].ID, secondIssues[len(secondIssues)-1].ID
	if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Action: "reparent", EpicID: first.ID, IssueID: factoryIssueID(t, db, first.ID, "mol"), ParentID: firstID}); err == nil {
		t.Fatal("hierarchy cycle was accepted")
	}
	if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Action: "link", EpicID: first.ID, IssueID: firstID, DependsOnID: secondID, DependencyType: "blocks"}); err != nil {
		t.Fatal(err)
	}
	if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Action: "link", EpicID: first.ID, IssueID: firstID, DependsOnID: secondID, DependencyType: "blocks"}); err != nil {
		t.Fatalf("duplicate link was not idempotent: %v", err)
	}
	if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Action: "link", EpicID: second.ID, IssueID: secondID, DependsOnID: firstID, DependencyType: "blocks"}); err == nil {
		t.Fatal("cycle was accepted")
	}
	if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Action: "unlink", EpicID: first.ID, IssueID: firstID, DependsOnID: secondID, DependencyType: "blocks"}); err != nil {
		t.Fatal(err)
	}
	if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Action: "link", EpicID: second.ID, IssueID: secondID, DependsOnID: firstID, DependencyType: "blocks"}); err != nil {
		t.Fatal(err)
	}
	secondIssues, err = db.ListFactoryIssues(ctx, second.ID)
	if err != nil || secondIssues[len(secondIssues)-1].DispatchState != "waiting" || len(secondIssues[len(secondIssues)-1].Blockers) != 1 || secondIssues[len(secondIssues)-1].Blockers[0].EpicID != first.ID {
		t.Fatalf("cross-Epic blocker evidence = %#v, %v", secondIssues, err)
	}
	if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Action: "delete", EpicID: first.ID, IssueID: firstID, Actor: "user"}); err != nil {
		t.Fatal(err)
	}
	issues, err := db.ListFactoryIssues(ctx, first.ID)
	if err != nil || len(issues) != len(firstIssues)-1 {
		t.Fatalf("issues after delete = %#v, %v", issues, err)
	}
	removed, err := db.ListRemovedFactoryIssues(ctx, first.ID)
	if err != nil || len(removed) != 1 || removed[0].ID != firstID || removed[0].RemovedAt == 0 {
		t.Fatalf("removed audit = %#v, %v", removed, err)
	}
	var edges int
	if err := db.db.QueryRow(`SELECT count(*) FROM factory_issue_dependency WHERE issue_id = ? OR depends_on_issue_id = ?`, firstID, firstID).Scan(&edges); err != nil || edges != 1 {
		t.Fatalf("deleted issue lost dependency audit history: %d, %v", edges, err)
	}
	secondIssues, err = db.ListFactoryIssues(ctx, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if secondIssues[len(secondIssues)-1].DispatchState != "ready" {
		t.Fatalf("deleted cross-Epic blocker remained active: %#v", secondIssues[len(secondIssues)-1])
	}
}

// A node reparented onto itself or linked to itself is a one-node cycle the
// descendant/ancestor CTEs never see (they seed from the neighbours, not the
// node). Left in place it makes every issue listing spin forever.
func TestFactoryGraphMutationsRejectSelfReference(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	ctx := context.Background()
	epic, err := db.CreateFactoryEpic(ctx, "Self", "", "/repo", "", nativeTracerFormula(t))
	if err != nil {
		t.Fatal(err)
	}
	mol := factoryIssueID(t, db, epic.ID, "mol")
	if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Action: "create", EpicID: epic.ID, ParentID: mol, Kind: "implementation", Title: "Work"}); err != nil {
		t.Fatal(err)
	}
	issues, err := db.ListFactoryIssues(ctx, epic.ID)
	if err != nil {
		t.Fatal(err)
	}
	work := issues[len(issues)-1].ID
	if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Action: "reparent", EpicID: epic.ID, IssueID: work, ParentID: work}); !errors.Is(err, model.ErrInvalidGraphMutation) {
		t.Fatalf("self-reparent error = %v", err)
	}
	if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Action: "link", EpicID: epic.ID, IssueID: work, DependsOnID: work, DependencyType: "blocks"}); err == nil {
		t.Fatal("self-link was accepted")
	}
	done := make(chan error, 1)
	go func() { _, err := db.ListFactoryIssues(ctx, epic.ID); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ListFactoryIssues hung after self-reference mutation")
	}
	after, err := db.ListFactoryIssues(ctx, epic.ID)
	if err != nil || after[len(after)-1].ParentID != mol || after[len(after)-1].DispatchState != "ready" {
		t.Fatalf("self-reference leaked into the graph: %#v, %v", after[len(after)-1], err)
	}
}

func TestFactoryGraphMutationRejectsStartedOrClosedIssues(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	ctx := context.Background()
	first, err := db.CreateFactoryEpic(ctx, "First", "", "/repo", "", nativeTracerFormula(t))
	if err != nil {
		t.Fatal(err)
	}
	second, err := db.CreateFactoryEpic(ctx, "Second", "", "/repo", "", nativeTracerFormula(t))
	if err != nil {
		t.Fatal(err)
	}
	firstMol := factoryIssueID(t, db, first.ID, "mol")
	secondMol := factoryIssueID(t, db, second.ID, "mol")
	if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Action: "create", EpicID: first.ID, ParentID: firstMol, Kind: "task", Title: "Work"}); err != nil {
		t.Fatal(err)
	}
	workID := factoryChildIssueID(t, db, firstMol, 2)
	for _, mutation := range []model.GraphMutation{
		{Action: "edit", EpicID: second.ID, IssueID: workID, Title: "Wrong Epic"},
		{Action: "link", EpicID: second.ID, IssueID: workID, DependsOnID: secondMol, DependencyType: "blocks"},
	} {
		if err := db.MutateFactoryGraph(ctx, mutation); err == nil {
			t.Fatalf("accepted out-of-scope mutation %#v", mutation)
		}
	}
	for _, status := range []string{"deferred", "retry_wait", "pinned"} {
		if _, err := db.db.Exec(`UPDATE factory_issue SET status = ? WHERE id = ?`, status, workID); err != nil {
			t.Fatal(err)
		}
		if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Action: "edit", EpicID: first.ID, IssueID: workID, Title: "Still mutable"}); err != nil {
			t.Fatalf("edit %s issue: %v", status, err)
		}
	}
	if _, err := db.db.Exec(`UPDATE factory_issue SET status = 'open' WHERE id = ?`, workID); err != nil {
		t.Fatal(err)
	}
	if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Action: "create", EpicID: first.ID, ParentID: firstMol, Kind: "mol", Title: "Child"}); err != nil {
		t.Fatal(err)
	}
	childMolID := factoryChildIssueID(t, db, firstMol, 3)
	if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Action: "create", EpicID: first.ID, ParentID: childMolID, Kind: "task", Title: "Descendant"}); err != nil {
		t.Fatal(err)
	}
	if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Action: "delete", EpicID: first.ID, IssueID: childMolID}); err != nil {
		t.Fatal(err)
	}
	for _, issue := range mustListFactoryIssues(t, db, first.ID) {
		if issue.ID == childMolID || issue.ParentID == childMolID {
			t.Fatalf("soft-deleted Mol descendant remained active: %#v", issue)
		}
	}
	if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Action: "create", EpicID: first.ID, ParentID: firstMol, Kind: "task", Title: "Deferred deletion"}); err != nil {
		t.Fatal(err)
	}
	deferredID := factoryChildIssueID(t, db, firstMol, 4)
	if _, err := db.db.Exec(`UPDATE factory_issue SET status = 'deferred' WHERE id = ?`, deferredID); err != nil {
		t.Fatal(err)
	}
	if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Action: "delete", EpicID: first.ID, IssueID: deferredID}); err != nil {
		t.Fatalf("delete deferred issue: %v", err)
	}
	if _, err := db.db.Exec(`UPDATE factory_issue SET status = 'in_progress' WHERE id = ?`, workID); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"edit", "delete"} {
		if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Action: action, EpicID: first.ID, IssueID: workID, Title: "Blocked"}); err == nil {
			t.Fatalf("%s in-progress issue", action)
		}
	}
	if _, err := db.db.Exec(`UPDATE factory_issue SET status = 'closed' WHERE id = ?`, workID); err != nil {
		t.Fatal(err)
	}
	if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Action: "edit", EpicID: first.ID, IssueID: workID, Title: "Blocked"}); err == nil {
		t.Fatal("edited closed issue")
	}
}

func TestFactoryGraphMutationRollsBackOnAuditFailure(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	ctx := context.Background()
	epic, err := db.CreateFactoryEpic(ctx, "Ship", "", "/repo", "", nativeTracerFormula(t))
	if err != nil {
		t.Fatal(err)
	}
	rootID := factoryIssueID(t, db, epic.ID, "mol")
	if _, err := db.db.Exec(`CREATE TRIGGER fail_graph_audit BEFORE INSERT ON factory_audit_record BEGIN SELECT RAISE(ABORT, 'injected audit failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Action: "create", EpicID: epic.ID, ParentID: rootID, Kind: "task", Title: "Work"}); err == nil || !strings.Contains(err.Error(), "injected audit failure") {
		t.Fatalf("MutateFactoryGraph error = %v", err)
	}
	var children, audits int
	if err := db.db.QueryRow(`SELECT count(*) FROM factory_issue_hierarchy WHERE parent_issue_id = ?`, rootID).Scan(&children); err != nil {
		t.Fatal(err)
	}
	if err := db.db.QueryRow(`SELECT count(*) FROM factory_audit_record WHERE epic_id = ?`, epic.ID).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if children != 3 || audits != 0 {
		t.Fatalf("partial graph mutation: children=%d audits=%d", children, audits)
	}
}

func mustListFactoryIssues(t *testing.T, db *DB, epicID string) []model.NativeIssue {
	t.Helper()
	issues, err := db.ListFactoryIssues(context.Background(), epicID)
	if err != nil {
		t.Fatal(err)
	}
	return issues
}

func TestFactoryGraphIDsAreReadableAndNeverReuseChildIndices(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	ctx := context.Background()
	epic, err := db.CreateFactoryEpic(ctx, "Ship Factory IDs", "", "/repo", "", nativeTracerFormula(t))
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(epic.ID, "-")
	if len(parts) != 2 || parts[0] != "sfi" || len(parts[1]) != 4 {
		t.Fatalf("Epic ID = %q", epic.ID)
	}
	rootID := factoryIssueID(t, db, epic.ID, "mol")
	planID := factoryIssueID(t, db, epic.ID, "plan")
	gateID := factoryIssueID(t, db, epic.ID, "gate")
	materializationID := factoryIssueID(t, db, epic.ID, "materialization")
	if rootID != epic.ID+".1" || planID != rootID+".1" || gateID != rootID+".2" || materializationID != rootID+".3" {
		t.Fatalf("Formula keys leaked into graph IDs: %q, %q, %q, %q", rootID, planID, gateID, materializationID)
	}
	for _, issue := range mustListFactoryIssues(t, db, epic.ID) {
		if issue.ID != rootID && issue.ParentID != rootID {
			t.Fatalf("dependency became hierarchy: %#v", issue)
		}
	}
	manifest := func(key string) string {
		encoded, err := json.Marshal(map[string]any{"epicId": epic.ID, "molId": rootID, "project": "/repo", "nodes": []map[string]string{{"key": key, "type": "implementation", "requirement": "required"}}})
		if err != nil {
			t.Fatal(err)
		}
		return string(encoded)
	}
	if _, err := db.MaterializeFactoryPlan(ctx, epic.ID, materializationID, "factory-materialize/v1", time.Now()); err == nil {
		t.Fatal("unapproved Plan materialized")
	}
	if _, err := db.DecideFactoryPlanGate(ctx, epic.ID, "invalid", 1, "hash", ""); err == nil {
		t.Fatal("invalid Plan gate action succeeded")
	}
	first, err := db.SaveFactoryProposalRevision(ctx, model.NativeProposalRevision{EpicID: epic.ID, MolID: rootID, Project: "/repo", ManifestJSON: manifest("first"), ContentHash: "first"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.DecideFactoryPlanGate(ctx, epic.ID, "approve", first.Revision, first.ContentHash, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := db.MaterializeFactoryPlan(ctx, epic.ID, materializationID, "wrong", time.Now()); err == nil {
		t.Fatal("wrong materialization profile succeeded")
	}
	firstResult, err := db.MaterializeFactoryPlan(ctx, epic.ID, materializationID, "factory-materialize/v1", time.Now())
	if err != nil || firstResult.ImplementationID != rootID+".4" || firstResult.ManifestKey != "first" {
		t.Fatalf("first materialization = %#v, %v", firstResult, err)
	}
	second, err := db.SaveFactoryProposalRevision(ctx, model.NativeProposalRevision{EpicID: epic.ID, MolID: rootID, Project: "/repo", ManifestJSON: manifest("second"), ContentHash: "second"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.DecideFactoryPlanGate(ctx, epic.ID, "approve", second.Revision, second.ContentHash, ""); err != nil {
		t.Fatal(err)
	}
	secondResult, err := db.MaterializeFactoryPlan(ctx, epic.ID, materializationID, "factory-materialize/v1", time.Now())
	if err != nil || secondResult.ImplementationID != rootID+".5" || secondResult.ManifestKey != "second" {
		t.Fatalf("second materialization = %#v, %v", secondResult, err)
	}
}

func factoryIssueID(t *testing.T, db *DB, epicID, kind string) string {
	t.Helper()
	issues, err := db.ListFactoryIssues(context.Background(), epicID)
	if err != nil {
		t.Fatal(err)
	}
	for _, issue := range issues {
		if issue.Kind == kind {
			return issue.ID
		}
	}
	t.Fatalf("missing %s issue", kind)
	return ""
}

func factoryChildIssueID(t *testing.T, db *DB, parentID string, index int) string {
	t.Helper()
	var id string
	if err := db.db.QueryRow(`SELECT child_issue_id FROM factory_issue_hierarchy WHERE parent_issue_id = ? AND child_index = ?`, parentID, index).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestFactoryServicePoursCreatedBuiltInEpic(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	formula := nativeTracerFormula(t)
	epic, err := db.CreateFactoryEpic(context.Background(), "Ship search", "Keep it small", "/repo", "", formula)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := factory.NewNative(db).Pour(context.Background(), epic.ID); err != nil {
		t.Fatalf("service Pour created built-in Epic: %v", err)
	}
}

func TestFactoryPourNormalizesLegacyBuiltInSourceHash(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	ctx := context.Background()
	formula := nativeTracerFormula(t)
	epic, err := db.CreateFactoryEpic(ctx, "Ship search", "Keep it small", "/repo", "", formula)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(formula.Source))
	legacyHash := hex.EncodeToString(sum[:])
	if _, err := db.db.Exec(`UPDATE factory_epic SET formula_hash = ? WHERE id = ?`, legacyHash, epic.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.PourFactoryEpic(ctx, epic.ID, formula); err != nil {
		t.Fatalf("PourFactoryEpic legacy built-in pin: %v", err)
	}
	got, err := db.GetFactoryEpic(ctx, epic.ID)
	if err != nil || got.FormulaHash != legacyHash {
		t.Fatalf("legacy pin changed = %#v, %v", got, err)
	}
}

func TestNativeFormulaRevisionsAreImmutableAndDeduplicated(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	ctx := context.Background()
	first, err := db.SaveNativeFactoryFormulaRevision(ctx, model.NativeFormulaRevision{FormulaID: "custom/team", Name: "Team", SourceTOML: "first", CompiledJSON: `{"name":"Team"}`, ContentHash: "hash-1"}, time.UnixMilli(1000))
	if err != nil || first.Revision != 1 {
		t.Fatalf("first save = %#v, %v", first, err)
	}
	repeated, err := db.SaveNativeFactoryFormulaRevision(ctx, model.NativeFormulaRevision{FormulaID: "custom/team", Name: "Renamed", SourceTOML: "changed", CompiledJSON: `{"name":"Renamed"}`, ContentHash: "hash-1"}, time.UnixMilli(2000))
	if err != nil || repeated.Revision != 1 || repeated.SourceTOML != "first" {
		t.Fatalf("repeated save = %#v, %v", repeated, err)
	}
	second, err := db.SaveNativeFactoryFormulaRevision(ctx, model.NativeFormulaRevision{FormulaID: "custom/team", Name: "Team v2", SourceTOML: "second", CompiledJSON: `{"name":"Team v2"}`, ContentHash: "hash-2"}, time.UnixMilli(3000))
	if err != nil || second.Revision != 2 {
		t.Fatalf("second save = %#v, %v", second, err)
	}
	history, err := db.GetNativeFactoryFormulaRevision(ctx, "custom/team", 1)
	if err != nil || history.SourceTOML != "first" || history.CompiledJSON != `{"name":"Team"}` {
		t.Fatalf("first revision mutated = %#v, %v", history, err)
	}
	current, err := db.GetNativeFactoryFormulaRevision(ctx, "custom/team", 0)
	if err != nil || current.Revision != 2 || current.SourceTOML != "second" {
		t.Fatalf("current revision = %#v, %v", current, err)
	}
	listed, err := db.ListNativeFactoryFormulaRevisions(ctx)
	if err != nil || len(listed) != 2 || listed[0].Revision != 1 || listed[1].Revision != 2 {
		t.Fatalf("listed revisions = %#v, %v", listed, err)
	}
}

func TestNativeFormulaRevisionFailureRollsBackFormulaRecord(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	if _, err := db.db.Exec(`CREATE TRIGGER fail_formula_revision BEFORE INSERT ON factory_native_formula_revision BEGIN SELECT RAISE(ABORT, 'injected revision failure'); END`); err != nil {
		t.Fatal(err)
	}
	_, err := db.SaveNativeFactoryFormulaRevision(context.Background(), model.NativeFormulaRevision{FormulaID: "custom/failure", Name: "Failure", SourceTOML: "source", CompiledJSON: `{}`, ContentHash: "hash"}, time.Now())
	if err == nil || !strings.Contains(err.Error(), "injected revision failure") {
		t.Fatalf("SaveNativeFactoryFormulaRevision error = %v", err)
	}
	for _, table := range []string{"factory_native_formula", "factory_native_formula_revision"} {
		var count int
		if err := db.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s count = %d, %v", table, count, err)
		}
	}
}

func TestFactoryPourPersistsNestedMolFormulaPins(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	ctx := context.Background()
	child := model.NativeFormula{ID: "custom/child", Version: 1, Source: "child", Hash: "same-hash", Inputs: []string{"goal", "initial_project"}, Nodes: []model.NativeFormulaNode{{Key: "plan", Kind: "plan"}}}
	parent := model.NativeFormula{ID: "custom/parent", Version: 1, Source: "parent", Hash: "same-hash", Inputs: []string{"goal", "initial_project"}, Nodes: []model.NativeFormulaNode{{Key: "plan", Kind: "plan"}}, Composition: []model.NativeFormulaComposition{{Key: "child", Requirement: "optional", Bindings: map[string]string{"goal": "goal", "initial_project": "initial_project"}, Formula: child}}}
	epic, err := db.CreateFactoryEpic(ctx, "Ship", "Brief", "/repo", "", parent)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.PourFactoryEpic(ctx, epic.ID, parent); err != nil {
		t.Fatal(err)
	}
	issues, err := db.ListFactoryIssues(ctx, epic.ID)
	if err != nil {
		t.Fatal(err)
	}
	var nested model.NativeIssue
	for _, issue := range issues {
		if issue.Kind == "mol" && issue.ID != factoryIssueID(t, db, epic.ID, "mol") {
			nested = issue
		}
	}
	if nested.ParentID != factoryIssueID(t, db, epic.ID, "mol") || nested.Requirement != "optional" || nested.FormulaID != child.ID || nested.FormulaVersion != child.Version || nested.FormulaHash != child.Hash || nested.Bindings["goal"] != "Ship" || nested.Bindings["initial_project"] != "/repo" {
		t.Fatalf("nested Mol = %#v", nested)
	}
	var pins int
	if err := db.db.QueryRow(`SELECT count(*) FROM factory_mol_formula`).Scan(&pins); err != nil || pins != 2 {
		t.Fatalf("Formula pins = %d, %v", pins, err)
	}
	var source string
	if err := db.db.QueryRow(`SELECT source_toml FROM factory_formula_identity WHERE formula_id = ? AND version = ?`, child.ID, child.Version).Scan(&source); err != nil || source != child.Source {
		t.Fatalf("child Formula source = %q, %v", source, err)
	}
}

func TestFactoryPourRejectsChangedPinAndRollsBack(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	ctx := context.Background()
	pinned := model.NativeFormula{ID: "custom/parent", Version: 1, Source: "pinned", Hash: "pinned-hash", Nodes: []model.NativeFormulaNode{{Key: "plan", Kind: "plan"}}}
	invalid := pinned
	invalid.Edges = []model.NativeFormulaEdge{{From: "missing", To: "plan"}}
	if _, err := db.CreateFactoryEpic(ctx, "Ship", "Brief", "/repo", "", invalid); err == nil {
		t.Fatal("accepted an invalid Formula graph")
	}
	for _, table := range []string{"factory_issue", "factory_issue_hierarchy", "factory_issue_dependency", "factory_mol_formula"} {
		var count int
		if err := db.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s count = %d, %v", table, count, err)
		}
	}
	epic, err := db.CreateFactoryEpic(ctx, "Ship", "Brief", "/repo", "", pinned)
	if err != nil {
		t.Fatal(err)
	}
	changed := pinned
	changed.Hash = "changed-hash"
	if _, _, err := db.PourFactoryEpic(ctx, epic.ID, changed); err == nil {
		t.Fatal("accepted a changed Formula pin")
	}
}

func TestFactoryCreationRejectsInvalidFormulaKeyBeforeWrites(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	child := model.NativeFormula{ID: "custom/child", Version: 1, Source: "child", Hash: "child-hash", Nodes: []model.NativeFormulaNode{{Key: "plan", Kind: "plan"}}}
	formula := model.NativeFormula{ID: "custom/parent", Version: 1, Source: "source", Hash: "hash", Nodes: []model.NativeFormulaNode{{Key: "plan", Kind: "plan"}}, Composition: []model.NativeFormulaComposition{{Key: "not valid", Formula: child}}}
	if _, err := db.CreateFactoryEpic(context.Background(), "Ship", "Brief", "/repo", "", formula); err == nil {
		t.Fatal("accepted an invalid Formula key")
	}
	for _, table := range []string{"factory_project", "factory_epic", "factory_formula_identity", "factory_issue", "factory_issue_hierarchy", "factory_mol_formula"} {
		var count int
		if err := db.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s count = %d, %v", table, count, err)
		}
	}
}

func TestFactoryPourRollsBackNestedStorageFailure(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	child := model.NativeFormula{ID: "custom/child", Version: 1, Source: "child", Hash: "child-hash", Inputs: []string{"goal"}, Nodes: []model.NativeFormulaNode{{Key: "plan", Kind: "plan"}}}
	parent := model.NativeFormula{ID: "custom/parent", Version: 1, Source: "parent", Hash: "parent-hash", Inputs: []string{"goal"}, Nodes: []model.NativeFormulaNode{{Key: "plan", Kind: "plan"}}, Composition: []model.NativeFormulaComposition{{Key: "child", Bindings: map[string]string{"goal": "goal"}, Formula: child}}}
	if _, err := db.db.Exec(`CREATE TRIGGER fail_nested_pin BEFORE INSERT ON factory_mol_formula WHEN NEW.mol_id LIKE '%.2' BEGIN SELECT RAISE(ABORT, 'nested pin failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateFactoryEpic(context.Background(), "Ship", "Brief", "/repo", "", parent); err == nil || !strings.Contains(err.Error(), "nested pin failure") {
		t.Fatalf("CreateFactoryEpic error = %v, want nested storage failure", err)
	}
	for _, table := range []string{"factory_epic", "factory_issue", "factory_issue_hierarchy", "factory_mol_formula"} {
		var count int
		if err := db.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s count = %d, %v", table, count, err)
		}
	}
}

func TestFactoryEpicCreationIsIdempotentByInstantiationID(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	formula := nativeTracerFormula(t)
	first, err := db.CreateFactoryEpic(context.Background(), "Ship", "Brief", "/repo", "intake-1", formula)
	if err != nil {
		t.Fatal(err)
	}
	second, err := db.CreateFactoryEpic(context.Background(), "Ship", "Brief", "/repo", "intake-1", formula)
	if err != nil || second.ID != first.ID {
		t.Fatalf("second create = %#v, %v", second, err)
	}
	if _, err := db.CreateFactoryEpic(context.Background(), "Changed", "Brief", "/repo", "intake-1", formula); !errors.Is(err, model.ErrNativeInstantiationConflict) {
		t.Fatalf("mismatched create error = %v", err)
	}
}

func TestFactoryMaterializationIsAtomicAndIdempotent(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	ctx := context.Background()
	materialize := func(instantiation string) (model.NativeEpic, model.NativeMaterialization, error) {
		epic, err := db.CreateFactoryEpic(ctx, "Ship "+instantiation, "Brief", "/repo", instantiation, nativeTracerFormula(t))
		if err != nil {
			return model.NativeEpic{}, model.NativeMaterialization{}, err
		}
		manifest, err := json.Marshal(map[string]any{"epicId": epic.ID, "molId": factoryIssueID(t, db, epic.ID, "mol"), "project": "/repo", "nodes": []map[string]string{{"key": "implement", "type": "implementation", "requirement": "required"}}})
		if err != nil {
			return model.NativeEpic{}, model.NativeMaterialization{}, err
		}
		proposal, err := db.SaveFactoryProposalRevision(ctx, model.NativeProposalRevision{EpicID: epic.ID, MolID: factoryIssueID(t, db, epic.ID, "mol"), Project: "/repo", ManifestJSON: string(manifest), ContentHash: "approved-" + instantiation})
		if err != nil {
			return model.NativeEpic{}, model.NativeMaterialization{}, err
		}
		if _, err := db.DecideFactoryPlanGate(ctx, epic.ID, "approve", proposal.Revision, proposal.ContentHash, ""); err != nil {
			return model.NativeEpic{}, model.NativeMaterialization{}, err
		}
		result, err := db.MaterializeFactoryPlan(ctx, epic.ID, factoryIssueID(t, db, epic.ID, "materialization"), "factory-materialize/v1", time.Now())
		return epic, result, err
	}
	_, first, err := materialize("first")
	if err != nil || first.ImplementationID == "" {
		t.Fatalf("materialize = %#v, %v", first, err)
	}
	second, err := db.MaterializeFactoryPlan(ctx, first.EpicID, first.IssueID, "factory-materialize/v1", time.Now())
	if err != nil || second != first {
		t.Fatalf("repeated materialize = %#v, %v", second, err)
	}
	if _, err := db.db.Exec(`UPDATE factory_plan_gate SET resolution = 'open' WHERE epic_id = ?`, first.EpicID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.MaterializeFactoryPlan(ctx, first.EpicID, first.IssueID, "factory-materialize/v1", time.Now()); err == nil {
		t.Fatal("reused materialization accepted a changed approval")
	}
	if _, err := db.db.Exec(`UPDATE factory_plan_gate SET resolution = 'approved' WHERE epic_id = ?`, first.EpicID); err != nil {
		t.Fatal(err)
	}
	for table, want := range map[string]int{"factory_materialization": 1, "factory_materialization_provenance": 3} {
		var count int
		if err := db.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&count); err != nil || count != want {
			t.Fatalf("%s count = %d, %v, want %d", table, count, err, want)
		}
	}
	if _, err := db.db.Exec(`CREATE TRIGGER fail_implementation BEFORE INSERT ON factory_issue WHEN NEW.kind = 'implementation' BEGIN SELECT RAISE(ABORT, 'injected failure'); END`); err != nil {
		t.Fatal(err)
	}
	failedEpic, failed, err := materialize("failed")
	if err == nil {
		t.Fatal("injected materialization failure succeeded")
	}
	var transactions, implementations int
	if err := db.db.QueryRow(`SELECT count(*) FROM factory_materialization WHERE epic_id = ?`, failedEpic.ID).Scan(&transactions); err != nil {
		t.Fatal(err)
	}
	if err := db.db.QueryRow(`SELECT count(*) FROM factory_issue WHERE epic_id = ? AND kind = 'implementation'`, failedEpic.ID).Scan(&implementations); err != nil || transactions != 0 || implementations != 0 || failed.ID != "" {
		t.Fatalf("partial materialization: transactions=%d implementations=%d result=%#v err=%v", transactions, implementations, failed, err)
	}
}

// Re-materializing an Epic supersedes its previous implementation. Work the
// user had already hung under that implementation must go with it: an orphan
// whose parent is removed would otherwise crash every issue listing.
func TestFactoryRematerializationRemovesSupersededDescendants(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	ctx := context.Background()
	epic, err := db.CreateFactoryEpic(ctx, "Ship", "Brief", "/repo", "", nativeTracerFormula(t))
	if err != nil {
		t.Fatal(err)
	}
	mol, materialization := factoryIssueID(t, db, epic.ID, "mol"), factoryIssueID(t, db, epic.ID, "materialization")
	approveAndMaterialize := func(hash string) model.NativeMaterialization {
		manifest, _ := json.Marshal(map[string]any{"epicId": epic.ID, "molId": mol, "project": "/repo", "nodes": []map[string]string{{"key": "implement", "type": "implementation", "requirement": "required"}}})
		proposal, err := db.SaveFactoryProposalRevision(ctx, model.NativeProposalRevision{EpicID: epic.ID, MolID: mol, Project: "/repo", ManifestJSON: string(manifest), ContentHash: hash})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.DecideFactoryPlanGate(ctx, epic.ID, "approve", proposal.Revision, proposal.ContentHash, ""); err != nil {
			t.Fatal(err)
		}
		result, err := db.MaterializeFactoryPlan(ctx, epic.ID, materialization, "factory-materialize/v1", time.Now())
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	first := approveAndMaterialize("first")
	if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Action: "create", EpicID: epic.ID, ParentID: first.ImplementationID, Kind: "task", Title: "Follow-up"}); err != nil {
		t.Fatal(err)
	}
	second := approveAndMaterialize("second")
	if second.ImplementationID == first.ImplementationID {
		t.Fatalf("second materialization reused implementation %s", first.ImplementationID)
	}
	issues, err := db.ListFactoryIssues(ctx, epic.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, issue := range issues {
		if issue.ID == first.ImplementationID || issue.ParentID == first.ImplementationID {
			t.Fatalf("superseded work still listed: %#v", issue)
		}
	}
	removed, err := db.ListRemovedFactoryIssues(ctx, epic.ID)
	if err != nil || len(removed) != 2 {
		t.Fatalf("removed = %#v, %v; want implementation and its child", removed, err)
	}
}

func TestFactoryIssueDispatchExplainsOutcomesAndDelays(t *testing.T) {
	ctx := context.Background()
	for _, tt := range []struct {
		name, edgeType, blockerKind, blockerOutcome, resolution, want string
	}{
		{"successful blocker", "blocks", "task", "succeeded", "", "ready"},
		{"open blocker", "blocks", "task", "", "", "waiting"},
		{"failed blocker", "blocks", "task", "failed", "", "terminally_blocked"},
		{"failure recovery", "on_failure", "task", "failed", "", "ready"},
		{"successful recovery skip", "on_failure", "task", "succeeded", "", "not_applicable"},
		{"cancelled recovery skip", "on_failure", "task", "cancelled", "", "not_applicable"},
		{"approved gate", "blocks", "gate", "succeeded", "approved", "ready"},
		{"rejected gate recovery", "on_failure", "gate", "failed", "rejected", "ready"},
		{"successful gate recovery skip", "on_failure", "gate", "succeeded", "observed", "not_applicable"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			db := openTestStateDB(t)
			defer db.Close()
			formula := model.NativeFormula{ID: "test/" + strings.ReplaceAll(tt.name, " ", "-"), Version: 1, Source: tt.name, Hash: tt.name, Nodes: []model.NativeFormulaNode{{Key: "a", Kind: tt.blockerKind}, {Key: "b", Kind: "implementation"}}, Edges: []model.NativeFormulaEdge{{From: "b", To: "a", Type: tt.edgeType}}}
			epic, err := db.CreateFactoryEpic(ctx, "Ship", "", "/repo", "", formula)
			if err != nil {
				t.Fatal(err)
			}
			if err := db.UpsertFactoryLocalExecutionAck(ctx, "local", "/repo", "factory-implement", "v1", "operator", time.Now()); err != nil {
				t.Fatal(err)
			}
			rootID := factoryIssueID(t, db, epic.ID, "mol")
			blockerID := factoryChildIssueID(t, db, rootID, 1)
			workID := factoryChildIssueID(t, db, rootID, 2)
			if tt.blockerOutcome != "" {
				if _, err := db.db.Exec(`UPDATE factory_issue SET status = 'closed', outcome = ?, outcome_reason = 'finished' WHERE id = ?`, tt.blockerOutcome, blockerID); err != nil {
					t.Fatal(err)
				}
			}
			if tt.blockerKind == "gate" {
				if _, err := db.db.Exec(`INSERT INTO factory_plan_gate (epic_id, issue_id, proposal_revision, proposal_hash, outcome, resolution, updated_at) VALUES (?, ?, 1, 'hash', ?, ?, 0)`, epic.ID, blockerID, tt.blockerOutcome, tt.resolution); err != nil {
					t.Fatal(err)
				}
			}
			issues, err := db.ListFactoryIssues(ctx, epic.ID)
			if err != nil {
				t.Fatal(err)
			}
			for _, issue := range issues {
				if issue.ID == workID && issue.DispatchState != tt.want {
					t.Fatalf("dispatch state = %q, want %q (%#v)", issue.DispatchState, tt.want, issue)
				}
				if issue.ID == workID && (tt.want == "terminally_blocked" || tt.want == "not_applicable") && (len(issue.Blockers) != 1 || issue.Blockers[0].ID != blockerID || issue.Blockers[0].EpicID != epic.ID || issue.Blockers[0].Outcome != tt.blockerOutcome) {
					t.Fatalf("dispatch explanation = %#v", issue.Blockers)
				}
			}
			_, _, claimErr := db.ClaimFactoryImplementation(ctx, epic.ID, workID, "factory-implement/v1", time.Now())
			if (claimErr == nil) != (tt.want == "ready") {
				t.Fatalf("claim error = %v for dispatch state %q", claimErr, tt.want)
			}
		})
	}
}

func TestFactoryIssueDeferralAndRetryWake(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	ctx := context.Background()
	formula := model.NativeFormula{ID: "test/delay", Version: 1, Source: "delay", Hash: "delay", Nodes: []model.NativeFormulaNode{{Key: "work", Kind: "task"}}}
	epic, err := db.CreateFactoryEpic(ctx, "Ship", "", "/repo", "", formula)
	if err != nil {
		t.Fatal(err)
	}
	workID := factoryChildIssueID(t, db, factoryIssueID(t, db, epic.ID, "mol"), 1)
	if err := db.DeferFactoryIssue(ctx, epic.ID, workID, "waiting for review"); err != nil {
		t.Fatal(err)
	}
	if issues, _ := db.ListFactoryIssues(ctx, epic.ID); issues[1].DispatchState != "deferred" || issues[1].Status != "deferred" {
		t.Fatalf("deferred issue = %#v", issues[1])
	}
	if err := db.ResumeFactoryIssue(ctx, epic.ID, workID); err != nil {
		t.Fatal(err)
	}
	wakeAt := time.Now().Add(time.Hour)
	if err := db.RetryFactoryIssueAt(ctx, epic.ID, workID, wakeAt); err != nil {
		t.Fatal(err)
	}
	issues, err := db.ListFactoryIssues(ctx, epic.ID)
	if err != nil || issues[1].DispatchState != "retry_wait" || issues[1].RetryAttempts != 1 || issues[1].RetryAt != wakeAt.UnixMilli() {
		t.Fatalf("retry issue = %#v, %v", issues[1], err)
	}
	if err := db.WakeFactoryRetries(ctx, wakeAt.Add(-time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	issues, _ = db.ListFactoryIssues(ctx, epic.ID)
	if issues[1].Status != "retry_wait" {
		t.Fatalf("retry woke early: %#v", issues[1])
	}
	if err := db.WakeFactoryRetries(ctx, wakeAt); err != nil {
		t.Fatal(err)
	}
	issues, _ = db.ListFactoryIssues(ctx, epic.ID)
	if issues[1].Status != "open" || issues[1].DispatchState != "ready" {
		t.Fatalf("retry did not wake: %#v", issues[1])
	}
}

func TestFactoryReferenceDescendantsCannotBeClaimed(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	child := model.NativeFormula{ID: "test/reference-child", Version: 1, Source: "child", Hash: "child", Inputs: []string{"goal"}, Nodes: []model.NativeFormulaNode{{Key: "plan", Kind: "plan"}}}
	parent := model.NativeFormula{ID: "test/reference-parent", Version: 1, Source: "parent", Hash: "parent", Inputs: []string{"goal"}, Composition: []model.NativeFormulaComposition{{Key: "reference", Requirement: "reference", Bindings: map[string]string{"goal": "goal"}, Formula: child}}}
	epic, err := db.CreateFactoryEpic(context.Background(), "Ship", "", "/repo", "", parent)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.ClaimFactoryPlan(context.Background(), epic.ID, factoryIssueID(t, db, epic.ID, "plan"), "factory-plan/v1", time.Now()); err == nil {
		t.Fatal("claimed a reference descendant")
	}
}

func TestFactoryPlanClaimAndProposalInventory(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	ctx := context.Background()
	epic, err := db.CreateFactoryEpic(ctx, "Ship", "Brief", "/repo", "claim-and-proposals", nativeTracerFormula(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetFactoryEpic(ctx, "missing"); !errors.Is(err, model.ErrNativeEpicNotFound) {
		t.Fatalf("missing Epic error = %v", err)
	}
	if _, _, err := db.PourFactoryEpic(ctx, "missing", nativeTracerFormula(t)); !errors.Is(err, model.ErrNativeEpicNotFound) {
		t.Fatalf("missing Epic pour error = %v", err)
	}
	epics, err := db.ListFactoryEpics(ctx)
	if err != nil || len(epics) != 1 || epics[0].ID != epic.ID {
		t.Fatalf("ListFactoryEpics = %#v, %v", epics, err)
	}
	planID := factoryIssueID(t, db, epic.ID, "plan")
	closed, err := db.CreateFactoryEpic(ctx, "Closed", "", "/repo", "closed-plan", nativeTracerFormula(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec(`UPDATE factory_epic SET status = 'closed' WHERE id = ?`, closed.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.ClaimFactoryPlan(ctx, closed.ID, factoryIssueID(t, db, closed.ID, "plan"), "factory-plan/v1", time.Now()); err == nil {
		t.Fatal("claimed Plan from closed Epic")
	}
	claimedEpic, attempt, err := db.ClaimFactoryPlan(ctx, epic.ID, planID, "factory-plan/v1", time.UnixMilli(1_000))
	if err != nil || claimedEpic.ID != epic.ID || attempt.WorkID != planID || attempt.Sequence != 1 || attempt.FrozenPolicy.Repository != "/repo" {
		t.Fatalf("ClaimFactoryPlan = %#v, %#v, %v", claimedEpic, attempt, err)
	}
	if changed, err := db.ActivateFactoryAttempt(ctx, attempt.ID, model.PlanningSession{Platform: "opencode", ID: "session"}, time.Now()); err != nil || !changed {
		t.Fatalf("ActivateFactoryAttempt = %v, %v", changed, err)
	}
	if _, _, err := db.ClaimFactoryPlan(ctx, epic.ID, planID, "factory-plan/v1", time.Now()); err == nil {
		t.Fatal("claimed an in-progress Plan twice")
	}
	if _, _, err := db.ClaimFactoryPlan(ctx, "missing", planID, "factory-plan/v1", time.Now()); !errors.Is(err, model.ErrNativeEpicNotFound) {
		t.Fatalf("missing Epic claim error = %v", err)
	}

	for _, hash := range []string{"first", "second"} {
		if _, err := db.SaveFactoryProposalRevision(ctx, model.NativeProposalRevision{EpicID: epic.ID, MolID: factoryIssueID(t, db, epic.ID, "mol"), Project: "/repo", ManifestJSON: `{}`, RationaleMarkdown: hash + " rationale", ContentHash: hash}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.SaveFactoryProposalRevision(ctx, model.NativeProposalRevision{EpicID: "missing"}); !errors.Is(err, model.ErrNativeEpicNotFound) {
		t.Fatalf("missing Epic proposal error = %v", err)
	}
	gate, err := db.GetFactoryPlanGate(ctx, epic.ID)
	if err != nil || gate.ProposalRevision != 2 || gate.ProposalHash != "second" || gate.Resolution != "open" {
		t.Fatalf("GetFactoryPlanGate = %#v, %v", gate, err)
	}
	first, err := db.GetFactoryProposalRevision(ctx, epic.ID, 1)
	if err != nil || first.ContentHash != "first" {
		t.Fatalf("first proposal = %#v, %v", first, err)
	}
	latest, err := db.GetFactoryProposalRevision(ctx, epic.ID, 0)
	if err != nil || latest.Revision != 2 || latest.ContentHash != "second" {
		t.Fatalf("latest proposal = %#v, %v", latest, err)
	}
	proposals, err := db.ListFactoryProposalRevisions(ctx, epic.ID)
	if err != nil || len(proposals) != 2 || proposals[0].ContentHash != "first" || proposals[1].ContentHash != "second" {
		t.Fatalf("ListFactoryProposalRevisions = %#v, %v", proposals, err)
	}
	if _, err := db.GetFactoryProposalRevision(ctx, epic.ID, 99); err == nil {
		t.Fatal("missing proposal revision was returned")
	}
	revised, err := db.DecideFactoryPlanGate(ctx, epic.ID, "revise", latest.Revision, latest.ContentHash, "split the work")
	if err != nil || revised.Resolution != "revision_requested" || revised.Feedback != "split the work" {
		t.Fatalf("revision request = %#v, %v", revised, err)
	}
	if _, err := db.DecideFactoryPlanGate(ctx, epic.ID, "approve", latest.Revision, latest.ContentHash, ""); err == nil {
		t.Fatal("approved a proposal after requesting revision")
	}
	third, err := db.SaveFactoryProposalRevision(ctx, model.NativeProposalRevision{EpicID: epic.ID, MolID: factoryIssueID(t, db, epic.ID, "mol"), Project: "/repo", ManifestJSON: `{}`, ContentHash: "third"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.DecideFactoryPlanGate(ctx, epic.ID, "approve", latest.Revision, latest.ContentHash, ""); err == nil {
		t.Fatal("approved a stale proposal")
	}
	approved, err := db.DecideFactoryPlanGate(ctx, epic.ID, "approve", third.Revision, third.ContentHash, "")
	if err != nil || approved.Resolution != "approved" || approved.Outcome != "succeeded" {
		t.Fatalf("approval = %#v, %v", approved, err)
	}
	if repeated, err := db.DecideFactoryPlanGate(ctx, epic.ID, "approve", third.Revision, third.ContentHash, ""); err != nil || repeated.Resolution != approved.Resolution || repeated.Outcome != approved.Outcome {
		t.Fatalf("repeated approval = %#v, %v", repeated, err)
	}
}

func TestFactoryPlanRejectionClosesPendingWork(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	ctx := context.Background()
	epic, err := db.CreateFactoryEpic(ctx, "Reject", "", "/repo", "", nativeTracerFormula(t))
	if err != nil {
		t.Fatal(err)
	}
	planID := factoryIssueID(t, db, epic.ID, "plan")
	if _, err := db.db.Exec(`UPDATE factory_issue SET status = 'in_progress' WHERE id = ?`, planID); err != nil {
		t.Fatal(err)
	}
	proposal, err := db.SaveFactoryProposalRevision(ctx, model.NativeProposalRevision{EpicID: epic.ID, MolID: factoryIssueID(t, db, epic.ID, "mol"), Project: "/repo", ManifestJSON: `{}`, ContentHash: "rejected"})
	if err != nil {
		t.Fatal(err)
	}
	rejected, err := db.DecideFactoryPlanGate(ctx, epic.ID, "reject", proposal.Revision, proposal.ContentHash, "not viable")
	if err != nil || rejected.Resolution != "rejected" || rejected.Outcome != "failed" || len(rejected.ReviewIssueIDs) != 1 || rejected.ReviewIssueIDs[0] != planID {
		t.Fatalf("rejection = %#v, %v", rejected, err)
	}
	got, err := db.GetFactoryEpic(ctx, epic.ID)
	if err != nil || got.Status != "closed" {
		t.Fatalf("rejected Epic = %#v, %v", got, err)
	}
	if repeated, err := db.DecideFactoryPlanGate(ctx, epic.ID, "reject", proposal.Revision, proposal.ContentHash, "ignored"); err != nil || repeated.Resolution != rejected.Resolution || repeated.Outcome != rejected.Outcome {
		t.Fatalf("repeated rejection = %#v, %v", repeated, err)
	}
}

func TestFactoryMolClosureGuardsRequiredWorkAndCancelsOpenOptionalWork(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	ctx := context.Background()
	formula := model.NativeFormula{ID: "test/closure", Version: 1, Source: "closure", Hash: "closure", Nodes: []model.NativeFormulaNode{{Key: "required", Kind: "task"}, {Key: "optional", Kind: "task"}, {Key: "gate", Kind: "gate"}}}
	epic, err := db.CreateFactoryEpic(ctx, "Ship", "", "/repo", "", formula)
	if err != nil {
		t.Fatal(err)
	}
	rootID := factoryIssueID(t, db, epic.ID, "mol")
	optionalID := factoryChildIssueID(t, db, rootID, 2)
	if _, err := db.db.Exec(`UPDATE factory_issue_hierarchy SET requirement = 'optional' WHERE child_issue_id = ?`, optionalID); err != nil {
		t.Fatal(err)
	}
	if err := db.CloseFactoryMol(ctx, epic.ID, rootID); err == nil {
		t.Fatal("closed Mol with unfinished required work")
	}
	requiredID := factoryChildIssueID(t, db, rootID, 1)
	if _, err := db.db.Exec(`UPDATE factory_issue SET status = 'closed', outcome = 'succeeded' WHERE id = ?`, requiredID); err != nil {
		t.Fatal(err)
	}
	if err := db.CloseFactoryMol(ctx, epic.ID, rootID); err == nil {
		t.Fatal("closed Mol with an unsatisfied required Gate")
	}
	gateID := factoryIssueID(t, db, epic.ID, "gate")
	if _, err := db.db.Exec(`INSERT INTO factory_plan_gate (epic_id, issue_id, proposal_revision, proposal_hash, outcome, resolution, updated_at) VALUES (?, ?, 1, 'hash', 'succeeded', 'approved', 0)`, epic.ID, gateID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec(`UPDATE factory_issue SET status = 'closed', outcome = 'succeeded' WHERE id = ?`, gateID); err != nil {
		t.Fatal(err)
	}
	if err := db.CloseFactoryMol(ctx, epic.ID, rootID); err != nil {
		t.Fatalf("CloseFactoryMol: %v", err)
	}
	var status, outcome, reason string
	if err := db.db.QueryRow(`SELECT status, outcome, outcome_reason FROM factory_issue WHERE id = ?`, optionalID).Scan(&status, &outcome, &reason); err != nil || status != "closed" || outcome != "cancelled" || reason != "container_closed_without_execution" {
		t.Fatalf("optional closure = %q, %q, %q, %v", status, outcome, reason, err)
	}
	if err := db.CloseFactoryEpic(ctx, epic.ID); err != nil {
		t.Fatalf("CloseFactoryEpic: %v", err)
	}
}

func TestFactoryMolClosureRollsBackOptionalCancellationOnFailure(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	ctx := context.Background()
	formula := model.NativeFormula{ID: "test/closure-rollback", Version: 1, Source: "closure", Hash: "closure", Nodes: []model.NativeFormulaNode{{Key: "required", Kind: "task"}, {Key: "optional", Kind: "task"}}}
	epic, err := db.CreateFactoryEpic(ctx, "Ship", "", "/repo", "", formula)
	if err != nil {
		t.Fatal(err)
	}
	rootID := factoryIssueID(t, db, epic.ID, "mol")
	requiredID := factoryChildIssueID(t, db, rootID, 1)
	optionalID := factoryChildIssueID(t, db, rootID, 2)
	if _, err := db.db.Exec(`UPDATE factory_issue_hierarchy SET requirement = 'optional' WHERE child_issue_id = ?`, optionalID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec(`UPDATE factory_issue SET status = 'closed', outcome = 'succeeded' WHERE id = ?`, requiredID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec(`CREATE TRIGGER fail_mol_close BEFORE UPDATE OF status ON factory_issue WHEN NEW.id = '` + rootID + `' AND NEW.status = 'closed' BEGIN SELECT RAISE(ABORT, 'injected closure failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := db.CloseFactoryMol(ctx, epic.ID, rootID); err == nil || !strings.Contains(err.Error(), "injected closure failure") {
		t.Fatalf("CloseFactoryMol error = %v", err)
	}
	var status, outcome string
	if err := db.db.QueryRow(`SELECT status, outcome FROM factory_issue WHERE id = ?`, optionalID).Scan(&status, &outcome); err != nil {
		t.Fatal(err)
	}
	if status != "open" || outcome != "" {
		t.Fatalf("partial optional cancellation: status=%q outcome=%q", status, outcome)
	}
}

func nativeTracerFormula(t *testing.T) model.NativeFormula {
	t.Helper()
	formula := factory.BuiltInTracerFormula()
	return model.NativeFormula{
		ID: formula.ID, Version: formula.Version, Source: formula.Source, Hash: formula.Hash,
		Nodes: []model.NativeFormulaNode{{Key: "plan", Kind: "plan"}, {Key: "approval", Kind: "gate"}, {Key: "materialization", Kind: "materialization"}},
		Edges: []model.NativeFormulaEdge{{From: "approval", To: "plan", Type: "blocks"}, {From: "materialization", To: "approval", Type: "blocks"}},
	}
}
