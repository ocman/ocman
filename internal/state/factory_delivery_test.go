package state

import (
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory/model"
)

func TestFactoryDeliveryTracksRequiredGraph(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	ctx := t.Context()
	epic, err := db.CreateFactoryEpic(ctx, "", "Delivery", "", "/repo", "", nativeTracerFormula(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureFactoryDeliveryIssue(ctx, epic.ID); err != nil {
		t.Fatal(err)
	}
	mol := factoryIssueID(t, db, epic.ID, "mol")
	if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Action: "create", EpicID: epic.ID, ParentID: mol, Kind: "task", Title: "Work"}); err != nil {
		t.Fatal(err)
	}
	work := factoryIssueID(t, db, epic.ID, "task")
	for range 2 {
		if err := db.EnsureFactoryDeliveryIssue(ctx, epic.ID); err != nil {
			t.Fatal(err)
		}
	}
	delivery := factoryIssueID(t, db, epic.ID, "delivery")
	var count int
	if err := db.db.QueryRow(`SELECT count(*) FROM factory_issue WHERE epic_id = ? AND kind = 'delivery'`, epic.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("delivery count = %d, %v", count, err)
	}
	if err := db.db.QueryRow(`SELECT count(*) FROM factory_issue_dependency WHERE issue_id = ? AND depends_on_issue_id = ?`, delivery, work).Scan(&count); err != nil || count != 1 {
		t.Fatalf("dependency = %d, %v", count, err)
	}
	if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Action: "create", EpicID: epic.ID, ParentID: mol, Kind: "implementation", Title: "More work"}); err != nil {
		t.Fatal(err)
	}
	more := factoryIssueID(t, db, epic.ID, "implementation")
	if err := db.EnsureFactoryDeliveryIssue(ctx, epic.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.db.QueryRow(`SELECT count(*) FROM factory_issue_dependency WHERE issue_id = ? AND depends_on_issue_id = ?`, delivery, more).Scan(&count); err != nil || count != 1 {
		t.Fatalf("new dependency = %d, %v", count, err)
	}
	if _, err := db.db.Exec(`UPDATE factory_issue_hierarchy SET requirement = 'optional' WHERE child_issue_id = ?`, more); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureFactoryDeliveryIssue(ctx, epic.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.db.QueryRow(`SELECT count(*) FROM factory_issue_dependency WHERE issue_id = ? AND depends_on_issue_id = ?`, delivery, more).Scan(&count); err != nil || count != 0 {
		t.Fatalf("optional dependency = %d, %v", count, err)
	}
	if err := db.UpsertFactoryLocalExecutionAck(ctx, "local", "/repo", "factory-implement", "v1", "operator", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.ClaimFactoryImplementation(ctx, epic.ID, delivery, "factory-implement/v1", time.Now()); err == nil {
		t.Fatal("claimed delivery before required work")
	}
	if _, err := db.db.Exec(`UPDATE factory_issue SET status = 'in_progress' WHERE id = ?`, delivery); err != nil {
		t.Fatal(err)
	}
	if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Action: "create", EpicID: epic.ID, ParentID: mol, Kind: "task", Title: "Too late"}); err == nil {
		t.Fatal("changed graph during delivery")
	}
	if _, err := db.db.Exec(`UPDATE factory_issue SET status = 'closed', outcome = 'failed' WHERE id = ?`, delivery); err != nil {
		t.Fatal(err)
	}
	if err := db.ReopenFactoryIssue(ctx, epic.ID, delivery); err != nil {
		t.Fatal(err)
	}
}

func TestFactoryDeliveryTracksProjectsProgressively(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	ctx := t.Context()
	epic, err := db.CreateFactoryEpicWithProjects(ctx, "", "Delivery", "", "/repo", "", nativeTracerFormula(t), []string{"/other", "/optional", "/reference", "/idle"})
	if err != nil {
		t.Fatal(err)
	}
	mol := factoryIssueID(t, db, epic.ID, "mol")
	for _, work := range []model.GraphMutation{
		{Action: "create", EpicID: epic.ID, ParentID: mol, Kind: "task", Title: "Repo work", Project: "/repo"},
		{Action: "create", EpicID: epic.ID, ParentID: mol, Kind: "implementation", Title: "Other work", Project: "/other"},
		{Action: "create", EpicID: epic.ID, ParentID: mol, Kind: "task", Title: "Optional project work", Requirement: "optional", Project: "/optional"},
		{Action: "create", EpicID: epic.ID, ParentID: mol, Kind: "task", Title: "Reference project work", Requirement: "reference", Project: "/reference"},
	} {
		if err := db.MutateFactoryGraph(ctx, work); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.EnsureFactoryDeliveryIssue(ctx, epic.ID); err != nil {
		t.Fatal(err)
	}
	issues, err := db.ListFactoryIssues(ctx, epic.ID)
	if err != nil {
		t.Fatal(err)
	}
	deliveries := map[string]model.NativeIssue{}
	var otherWork string
	for _, issue := range issues {
		if issue.Kind == "delivery" {
			deliveries[issue.Project] = issue
		}
		if issue.Title == "Other work" {
			otherWork = issue.ID
		}
	}
	if len(deliveries) != 3 {
		t.Fatalf("deliveries = %#v", deliveries)
	}
	for project, delivery := range deliveries {
		for _, dependency := range delivery.DependsOn {
			for _, issue := range issues {
				if issue.ID == dependency.ID && issue.Project != project {
					t.Fatalf("delivery %s depends on %s project %s", project, dependency.ID, issue.Project)
				}
			}
		}
	}
	if _, err := db.db.Exec(`UPDATE factory_issue SET status = 'closed', outcome = 'succeeded' WHERE epic_id = ? AND kind NOT IN ('delivery', 'mol')`, epic.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec(`UPDATE factory_issue SET status = 'open', outcome = '' WHERE id = ?`, otherWork); err != nil {
		t.Fatal(err)
	}
	issues, err = db.ListFactoryIssues(ctx, epic.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, issue := range issues {
		if issue.Kind != "delivery" {
			continue
		}
		want := "waiting"
		if issue.Project == "/repo" || issue.Project == "/optional" {
			want = "ready"
		}
		if issue.DispatchState != want {
			t.Errorf("delivery %s state = %s, want %s", issue.Project, issue.DispatchState, want)
		}
	}

	if err := db.UpsertFactoryLocalExecutionAck(ctx, "local", "/repo", "factory-implement", "v1", "operator", time.Now()); err != nil {
		t.Fatal(err)
	}
	_, attempt, err := db.ClaimFactoryImplementation(ctx, epic.ID, deliveries["/repo"].ID, "factory-implement/v1", time.Now())
	if err != nil {
		t.Fatalf("upstream project was not progressively deliverable: %v", err)
	}
	if !attempt.FrozenPolicy.Delivery || attempt.FrozenPolicy.Repository != "/repo" {
		t.Fatalf("delivery attempt = %#v", attempt)
	}
	if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Action: "create", EpicID: epic.ID, ParentID: mol, Kind: "task", Title: "More other work", Project: "/other"}); err != nil {
		t.Fatalf("other project was sealed: %v", err)
	}
	if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Action: "create", EpicID: epic.ID, ParentID: mol, Kind: "task", Title: "Too late", Project: "/repo"}); err == nil {
		t.Fatal("delivery did not seal its project")
	}
}

func TestFactoryWorkspaceRequiresPreparedAttempt(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	if err := db.SetFactoryAttemptWorkspace(t.Context(), "missing", model.FactoryAttemptPolicy{}); err == nil {
		t.Fatal("accepted empty workspace")
	}
	if err := db.SetFactoryAttemptWorkspace(t.Context(), "missing", model.FactoryAttemptPolicy{Branch: "factory/x", TargetBranch: "main"}); err == nil {
		t.Fatal("accepted missing attempt")
	}
}

func TestFactoryDoesNotClaimWorkAfterDelivery(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	ctx := t.Context()
	epic, err := db.CreateFactoryEpic(ctx, "", "Delivered", "", "/repo", "", nativeTracerFormula(t))
	if err != nil {
		t.Fatal(err)
	}
	mol := factoryIssueID(t, db, epic.ID, "mol")
	if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Action: "create", EpicID: epic.ID, ParentID: mol, Kind: "task", Title: "Required"}); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureFactoryDeliveryIssue(ctx, epic.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Action: "create", EpicID: epic.ID, ParentID: mol, Kind: "implementation", Title: "Optional", Requirement: "optional"}); err != nil {
		t.Fatal(err)
	}
	optional := factoryIssueID(t, db, epic.ID, "implementation")
	if _, err := db.db.Exec(`UPDATE factory_issue SET status = 'closed', outcome = 'succeeded' WHERE epic_id = ? AND id <> ?`, epic.ID, optional); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertFactoryLocalExecutionAck(ctx, "local", "/repo", "factory-implement", "v1", "operator", time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"open", "deferred", "retry_wait"} {
		t.Run(status, func(t *testing.T) {
			if _, err := db.db.Exec(`UPDATE factory_issue SET status = ? WHERE id = ?`, status, optional); err != nil {
				t.Fatal(err)
			}
			issues, err := db.ListFactoryIssues(ctx, epic.ID)
			if err != nil {
				t.Fatal(err)
			}
			for _, issue := range issues {
				if issue.ID == optional && issue.DispatchState != "not_applicable" {
					t.Errorf("post-delivery dispatch state = %s", issue.DispatchState)
				}
			}
			if _, _, err := db.ClaimFactoryImplementation(ctx, epic.ID, optional, "factory-implement/v1", time.Now()); err == nil {
				t.Fatal("claimed implementation after final delivery")
			}
		})
	}
}
