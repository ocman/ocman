package state

import (
	"strings"
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

func TestFactoryDeliveryRefreshesThenCreatesSuccessor(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	ctx := t.Context()
	epic, err := db.CreateFactoryEpicWithProjects(ctx, "", "Delivery lineage", "", "/repo", "", nativeTracerFormula(t), []string{"/app"})
	if err != nil {
		t.Fatal(err)
	}
	mol := factoryIssueID(t, db, epic.ID, "mol")
	for _, mutation := range []model.GraphMutation{
		{Action: "create", EpicID: epic.ID, ParentID: mol, Kind: "task", Title: "Repo work", Project: "/repo"},
		{Action: "create", EpicID: epic.ID, ParentID: mol, Kind: "task", Title: "App work", Project: "/app"},
	} {
		if err := db.MutateFactoryGraph(ctx, mutation); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.EnsureFactoryDeliveryIssue(ctx, epic.ID); err != nil {
		t.Fatal(err)
	}
	issues, _ := db.ListFactoryIssues(ctx, epic.ID)
	var firstDelivery, appWork string
	for _, issue := range issues {
		if issue.Kind == "delivery" && issue.Project == "/repo" {
			firstDelivery = issue.ID
		}
		if issue.Title == "App work" {
			appWork = issue.ID
		}
	}
	if _, err := db.db.Exec(`UPDATE factory_issue SET status = 'closed', outcome = 'succeeded' WHERE id = ?`, firstDelivery); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec(`INSERT INTO factory_attempt (id, epic_id, work_item_id, sequence, phase, terminal_outcome, frozen_policy_json, result_json, created_at, updated_at, finished_at) VALUES ('delivery-1', ?, ?, 1, 'terminal', 'succeeded', '{"repository":"/repo","profile":"factory-implement/v1","branch":"factory/lineage","targetBranch":"main","delivery":true}', '{"prUrl":"https://forge.example/pulls/1","commitSha":"one"}', 1, 1, 1)`, epic.ID, firstDelivery); err != nil {
		t.Fatal(err)
	}
	if err := db.RecordFactoryDeliveryObservation(ctx, model.FactoryDeliveryObservation{DeliveryIssueID: firstDelivery, AttemptID: "delivery-1", PRURL: "https://forge.example/pulls/1", CommitSHA: "one", Status: "open", ObservedAt: 1}); err != nil {
		t.Fatal(err)
	}
	if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Action: "create", EpicID: epic.ID, ParentID: mol, Kind: "task", Title: "Refresh work", Project: "/repo"}); err != nil {
		t.Fatalf("adding work before merge: %v", err)
	}
	if err := db.EnsureFactoryDeliveryIssue(ctx, epic.ID); err != nil {
		t.Fatal(err)
	}
	var status, outcome string
	if err := db.db.QueryRow(`SELECT status, outcome FROM factory_issue WHERE id = ?`, firstDelivery).Scan(&status, &outcome); err != nil || status != "open" || outcome != "" {
		t.Fatalf("refreshed delivery = %q/%q, %v", status, outcome, err)
	}
	var deliveryCount int
	if err := db.db.QueryRow(`SELECT count(*) FROM factory_issue WHERE epic_id = ? AND project_path = '/repo' AND kind = 'delivery'`, epic.ID).Scan(&deliveryCount); err != nil || deliveryCount != 1 {
		t.Fatalf("refresh delivery count = %d, %v", deliveryCount, err)
	}
	if err := db.db.QueryRow(`SELECT count(*) FROM factory_merge_gate_observation WHERE delivery_issue_id = ?`, firstDelivery).Scan(&deliveryCount); err != nil || deliveryCount != 0 {
		t.Fatalf("stale refresh observation count = %d, %v", deliveryCount, err)
	}
	pending, err := db.ListFactoryMergeGateDeliveries(ctx, time.Now(), 8)
	if err != nil || len(pending) != 0 {
		t.Fatalf("open refresh was polled = %#v, %v", pending, err)
	}
	if err := db.RecordFactoryDeliveryObservation(ctx, model.FactoryDeliveryObservation{DeliveryIssueID: firstDelivery, AttemptID: "delivery-1", PRURL: "https://forge.example/pulls/1", CommitSHA: "one", Status: "merged", ObservedAt: 2}); err != nil {
		t.Fatal(err)
	}
	if err := db.db.QueryRow(`SELECT count(*) FROM factory_merge_gate_observation WHERE delivery_issue_id = ?`, firstDelivery).Scan(&deliveryCount); err != nil || deliveryCount != 0 {
		t.Fatalf("in-flight observation restored stale merge = %d, %v", deliveryCount, err)
	}
	if _, err := db.db.Exec(`UPDATE factory_issue SET status = 'closed', outcome = 'succeeded' WHERE project_path = '/repo' AND kind NOT IN ('delivery', 'mol')`); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertFactoryLocalExecutionAck(ctx, "local", "/repo", "factory-implement", "v1", "operator", time.Now()); err != nil {
		t.Fatal(err)
	}
	_, refresh, err := db.ClaimFactoryImplementation(ctx, epic.ID, firstDelivery, "factory-implement/v1", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if refresh.FrozenPolicy.Branch != "factory/lineage" || refresh.FrozenPolicy.TargetBranch != "main" {
		t.Fatalf("refresh workspace = %#v", refresh.FrozenPolicy)
	}
	for _, statement := range []struct {
		query string
		arg   string
	}{
		{`DELETE FROM factory_external_mapping WHERE entity_id = ?`, refresh.ID},
		{`DELETE FROM factory_attempt WHERE id = ?`, refresh.ID},
		{`UPDATE factory_issue SET status = 'closed', outcome = 'succeeded' WHERE id = ?`, firstDelivery},
	} {
		if _, err := db.db.Exec(statement.query, statement.arg); err != nil {
			t.Fatal(err)
		}
	}

	if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Action: "link", EpicID: epic.ID, IssueID: appWork, DependsOnID: firstDelivery, DependencyType: "merge_gated"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec(`UPDATE factory_issue SET status = 'closed', outcome = 'succeeded' WHERE id = ?`, firstDelivery); err != nil {
		t.Fatal(err)
	}
	if err := db.RecordFactoryDeliveryObservation(ctx, model.FactoryDeliveryObservation{DeliveryIssueID: firstDelivery, AttemptID: "delivery-1", PRURL: "https://forge.example/pulls/1", CommitSHA: "one", Status: "merged", ObservedAt: 3}); err != nil {
		t.Fatal(err)
	}
	if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Action: "create", EpicID: epic.ID, ParentID: mol, Kind: "task", Title: "Successor work", Project: "/repo"}); err != nil {
		t.Fatalf("adding work after merge: %v", err)
	}
	if err := db.EnsureFactoryDeliveryIssue(ctx, epic.ID); err != nil {
		t.Fatal(err)
	}
	issues, err = db.ListFactoryIssues(ctx, epic.ID)
	if err != nil {
		t.Fatal(err)
	}
	var successor, successorWork string
	for _, issue := range issues {
		if issue.Kind == "delivery" && issue.Project == "/repo" && issue.ID != firstDelivery {
			successor = issue.ID
		}
		if issue.Title == "Successor work" {
			successorWork = issue.ID
		}
	}
	if successor == "" {
		t.Fatal("successor delivery was not created")
	}
	var pinned int
	if err := db.db.QueryRow(`SELECT count(*) FROM factory_issue_dependency WHERE issue_id = ? AND depends_on_issue_id = ? AND type = 'merge_gated'`, appWork, firstDelivery).Scan(&pinned); err != nil || pinned != 1 {
		t.Fatalf("existing merge gate moved = %d, %v", pinned, err)
	}
	if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Action: "link", EpicID: epic.ID, IssueID: appWork, DependsOnID: successor, DependencyType: "merge_gated"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec(`UPDATE factory_issue SET status = 'closed', outcome = 'succeeded' WHERE project_path = '/repo' AND kind = 'task'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec(`UPDATE factory_issue SET status = 'open', outcome = '' WHERE id = ?`, successorWork); err != nil {
		t.Fatal(err)
	}
	_, attempt, err := db.ClaimFactoryImplementation(ctx, epic.ID, successorWork, "factory-implement/v1", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if attempt.FrozenPolicy.Branch != "" || attempt.FrozenPolicy.CheckpointSHA != "" || attempt.FrozenPolicy.TargetBranch != "main" {
		t.Fatalf("successor workspace = %#v", attempt.FrozenPolicy)
	}
}

func TestFactoryMergeGateUsesForgeObservation(t *testing.T) {
	for _, tt := range []struct {
		status, want string
	}{
		{"merged", "ready"},
		{"open", "waiting"},
		{"draft", "waiting"},
		{"closed", "terminally_blocked"},
		{"unavailable", "waiting"},
	} {
		t.Run(tt.status, func(t *testing.T) {
			db := openTestStateDB(t)
			defer db.Close()
			ctx := t.Context()
			epic, err := db.CreateFactoryEpicWithProjects(ctx, "", "Merge gate", "", "/app", "", nativeTracerFormula(t), []string{"/sdk"})
			if err != nil {
				t.Fatal(err)
			}
			mol := factoryIssueID(t, db, epic.ID, "mol")
			for _, mutation := range []model.GraphMutation{
				{Action: "create", EpicID: epic.ID, ParentID: mol, Kind: "task", Title: "SDK", Project: "/sdk"},
				{Action: "create", EpicID: epic.ID, ParentID: mol, Kind: "task", Title: "App", Project: "/app"},
			} {
				if err := db.MutateFactoryGraph(ctx, mutation); err != nil {
					t.Fatal(err)
				}
			}
			if err := db.EnsureFactoryDeliveryIssue(ctx, epic.ID); err != nil {
				t.Fatal(err)
			}
			issues, _ := db.ListFactoryIssues(ctx, epic.ID)
			var deliveryID, appID, sdkID string
			for _, issue := range issues {
				if issue.Kind == "delivery" && issue.Project == "/sdk" {
					deliveryID = issue.ID
				}
				if issue.Title == "App" {
					appID = issue.ID
				}
				if issue.Title == "SDK" {
					sdkID = issue.ID
				}
			}
			if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Action: "link", EpicID: epic.ID, IssueID: sdkID, DependsOnID: deliveryID, DependencyType: "merge_gated"}); err == nil {
				t.Fatal("same-project merge gate was accepted")
			}
			if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Action: "link", EpicID: epic.ID, IssueID: appID, DependsOnID: deliveryID, DependencyType: "merge_gated"}); err != nil {
				t.Fatal(err)
			}
			if _, err := db.db.Exec(`UPDATE factory_issue SET status = 'closed', outcome = 'succeeded' WHERE id = ?`, deliveryID); err != nil {
				t.Fatal(err)
			}
			if _, err := db.db.Exec(`INSERT INTO factory_attempt (id, epic_id, work_item_id, sequence, phase, terminal_outcome, frozen_policy_json, result_json, created_at, updated_at, finished_at) VALUES ('delivery-attempt', ?, ?, 1, 'terminal', 'succeeded', '{"repository":"/sdk","profile":"factory-implement/v1"}', '{"prUrl":"https://forge.example/pulls/1","commitSha":"abc"}', 1, 1, 1)`, epic.ID, deliveryID); err != nil {
				t.Fatal(err)
			}
			reason := ""
			if tt.status == "unavailable" {
				reason = "Forge could not verify the Project Delivery PR"
			}
			if err := db.RecordFactoryDeliveryObservation(ctx, model.FactoryDeliveryObservation{DeliveryIssueID: deliveryID, AttemptID: "delivery-attempt", PRURL: "https://forge.example/pulls/1", CommitSHA: "abc", Status: tt.status, Reason: reason, ObservedAt: time.Now().UnixMilli()}); err != nil {
				t.Fatal(err)
			}
			if tt.status == "merged" {
				if err := db.RecordFactoryDeliveryObservation(ctx, model.FactoryDeliveryObservation{DeliveryIssueID: deliveryID, AttemptID: "delivery-attempt", PRURL: "https://forge.example/pulls/1", CommitSHA: "abc", Status: "open", ObservedAt: 1}); err != nil {
					t.Fatal(err)
				}
				var status string
				if err := db.db.QueryRow(`SELECT status FROM factory_merge_gate_observation WHERE delivery_issue_id = ?`, deliveryID).Scan(&status); err != nil || status != "merged" {
					t.Fatalf("merged observation regressed to %q, %v", status, err)
				}
			}
			if err := db.RecordFactoryDeliveryObservation(ctx, model.FactoryDeliveryObservation{DeliveryIssueID: deliveryID, AttemptID: "delivery-attempt", PRURL: "https://forge.example/pulls/1", CommitSHA: "abc", Status: tt.status, Reason: reason, ObservedAt: time.Now().UnixMilli()}); err != nil {
				t.Fatal(err)
			}
			var observationCount int
			if err := db.db.QueryRow(`SELECT count(*) FROM factory_merge_gate_observation WHERE delivery_issue_id = ?`, deliveryID).Scan(&observationCount); err != nil || observationCount != 1 {
				t.Fatalf("observation count = %d, %v", observationCount, err)
			}
			pending, err := db.ListFactoryMergeGateDeliveries(ctx, time.Now().Add(-30*time.Second), 8)
			if err != nil || (tt.status != "merged" && len(pending) != 0) {
				t.Fatalf("throttled observations = %#v, %v", pending, err)
			}
			issues, err = db.ListFactoryIssues(ctx, epic.ID)
			if err != nil {
				t.Fatal(err)
			}
			for _, issue := range issues {
				if issue.ID != appID {
					continue
				}
				if issue.DispatchState != tt.want || (tt.status != "merged" && (len(issue.Blockers) != 1 || issue.Blockers[0].Type != "merge_gated")) {
					t.Fatalf("merge-gated issue = %#v, want %s", issue, tt.want)
				}
				if tt.status == "closed" && !strings.Contains(issue.Blockers[0].Reason, "closed without merge") {
					t.Fatalf("closed PR reason = %q", issue.Blockers[0].Reason)
				}
			}
			if err := db.UpsertFactoryLocalExecutionAck(ctx, "local", "/app", "factory-implement", "v1", "operator", time.Now()); err != nil {
				t.Fatal(err)
			}
			_, _, claimErr := db.ClaimFactoryImplementation(ctx, epic.ID, appID, "factory-implement/v1", time.Now())
			if (claimErr == nil) != (tt.status == "merged") {
				t.Fatalf("claim error = %v", claimErr)
			}
		})
	}
}

func TestFactoryMergeGateRequiresDelivery(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	ctx := t.Context()
	epic, err := db.CreateFactoryEpic(ctx, "", "Gate", "", "/repo", "", nativeTracerFormula(t))
	if err != nil {
		t.Fatal(err)
	}
	mol := factoryIssueID(t, db, epic.ID, "mol")
	for _, title := range []string{"First", "Second"} {
		if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Action: "create", EpicID: epic.ID, ParentID: mol, Kind: "task", Title: title}); err != nil {
			t.Fatal(err)
		}
	}
	issues, _ := db.ListFactoryIssues(ctx, epic.ID)
	first, second := issues[len(issues)-2].ID, issues[len(issues)-1].ID
	if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Action: "link", EpicID: epic.ID, IssueID: second, DependsOnID: first, DependencyType: "merge_gated"}); err == nil {
		t.Fatal("merge gate targeting non-delivery was accepted")
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
