package state

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory/model"
)

func TestFactoryAttemptLifecycle(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	ctx := context.Background()
	at := time.UnixMilli(1_000)
	policy := model.FactoryAttemptPolicy{PlanRevision: 1, PlanHash: "hash", TargetID: "work", Repository: "/repo", Profile: "factory-implement/v1"}

	first, err := db.CreatePreparedFactoryAttempt(ctx, "epic", "work", policy, at)
	if err != nil || first.Sequence != 1 || first.Phase != model.FactoryAttemptPrepared {
		t.Fatalf("first attempt = %#v, %v", first, err)
	}
	second, err := db.CreatePreparedFactoryAttempt(ctx, "epic", "work", policy, at)
	if err != nil || second.Sequence != 2 {
		t.Fatalf("second attempt = %#v, %v", second, err)
	}
	if changed, err := db.ActivateFactoryAttempt(ctx, first.ID, model.PlanningSession{Platform: "opencode"}, at); err == nil || changed {
		t.Fatalf("partial session activation = %v, %v", changed, err)
	}
	if changed, err := db.ActivateFactoryAttempt(ctx, first.ID, model.PlanningSession{Platform: "opencode", ID: "session"}, at); err != nil || !changed {
		t.Fatalf("activation = %v, %v", changed, err)
	}
	if changed, err := db.CompleteFactoryAttempt(ctx, first.ID, model.FactoryAttemptResult{SchemaVersion: 1, Summary: "done"}, at); err != nil || !changed {
		t.Fatalf("completion = %v, %v", changed, err)
	}
	if changed, err := db.CompleteFactoryAttempt(ctx, first.ID, model.FactoryAttemptResult{SchemaVersion: 1, Summary: "again"}, at); err != nil || changed {
		t.Fatalf("repeated completion = %v, %v", changed, err)
	}
	if changed, err := db.ActivateFactoryAttempt(ctx, first.ID, model.PlanningSession{}, at); err != nil || changed {
		t.Fatalf("terminal activation = %v, %v", changed, err)
	}
	if changed, err := db.FailFactoryAttempt(ctx, second.ID, model.FactoryAttemptFailure{}, at); err == nil || changed {
		t.Fatalf("empty failure = %v, %v", changed, err)
	}
	if changed, err := db.FailFactoryAttempt(ctx, second.ID, model.FactoryAttemptFailure{Type: "launch", Message: "failed"}, at); err != nil || !changed {
		t.Fatalf("failure = %v, %v", changed, err)
	}
	got, ok, err := db.GetFactoryAttempt(ctx, first.ID)
	if err != nil || !ok || got.Outcome != model.FactoryAttemptSucceeded || got.Result == nil || got.Result.Summary != "done" {
		t.Fatalf("GetFactoryAttempt = %#v, %v, %v", got, ok, err)
	}
	attempts, err := db.ListFactoryAttempts(ctx, "epic")
	failed := false
	for _, attempt := range attempts {
		failed = failed || attempt.Outcome == model.FactoryAttemptFailed
	}
	if err != nil || len(attempts) != 2 || !failed {
		t.Fatalf("ListFactoryAttempts = %#v, %v", attempts, err)
	}
	if _, ok, err := db.GetFactoryAttempt(ctx, "missing"); err != nil || ok {
		t.Fatalf("missing attempt = %v, %v", ok, err)
	}
}

func TestFactoryAttemptRecoveryAndAuthorityGates(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	ctx := context.Background()
	at := time.UnixMilli(1_000)
	epic, err := db.CreateFactoryEpic(ctx, "Epic", "", "/repo", "", nativeTracerFormula(t))
	if err != nil {
		t.Fatal(err)
	}
	molID, workID := factoryIssueID(t, db, epic.ID, "mol"), epic.ID+".1.99"
	if _, err := db.db.Exec(`INSERT INTO factory_issue (id, epic_id, kind, title, status, created_at) VALUES (?, ?, 'implementation', 'Work', 'open', 0)`, workID, epic.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec(`INSERT INTO factory_issue_hierarchy (parent_issue_id, child_issue_id, child_index, requirement) VALUES (?, ?, 99, 'required')`, molID, workID); err != nil {
		t.Fatal(err)
	}
	attempt, err := db.CreatePreparedFactoryAttempt(ctx, epic.ID, workID, model.FactoryAttemptPolicy{Repository: "/repo", Profile: "factory-implement/v1"}, at)
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := db.ActivateFactoryAttempt(ctx, attempt.ID, model.PlanningSession{Platform: "opencode", ID: "session"}, at); err != nil || !changed {
		t.Fatalf("activation = %v, %v", changed, err)
	}

	recovery, err := db.CreateFactoryRecoveryGate(ctx, attempt.ID, "Choose", "blocked", nil, at)
	if err != nil || recovery.Choices == nil {
		t.Fatalf("recovery gate = %#v, %v", recovery, err)
	}
	if got, ok, err := db.GetFactoryRecoveryGate(ctx, recovery.IssueID); err != nil || !ok || got.AttemptID != attempt.ID {
		t.Fatalf("GetFactoryRecoveryGate = %#v, %v, %v", got, ok, err)
	}
	if _, ok, err := db.GetFactoryRecoveryGate(ctx, "missing"); err != nil || ok {
		t.Fatalf("missing recovery gate = %v, %v", ok, err)
	}
	if duplicate, err := db.CreateFactoryRecoveryGate(ctx, attempt.ID, "Choose", "blocked", nil, at); err != nil || duplicate.IssueID != recovery.IssueID {
		t.Fatalf("duplicate recovery gate = %#v, %v", duplicate, err)
	}
	if paused, err := db.IsFactoryAttemptRecoveryPaused(ctx, attempt.ID); err != nil || !paused {
		t.Fatalf("recovery paused = %v, %v", paused, err)
	}

	authority, handled, err := db.CreateFactoryAuthorityEscalationGate(ctx, "session", "request", "external_directory", "/tmp", at)
	if err != nil || !handled || authority.Permission != "external_directory" {
		t.Fatalf("authority gate = %#v, %v, %v", authority, handled, err)
	}
	if got, ok, err := db.GetFactoryAuthorityEscalationGate(ctx, authority.IssueID); err != nil || !ok || got.AttemptID != attempt.ID {
		t.Fatalf("GetFactoryAuthorityEscalationGate = %#v, %v, %v", got, ok, err)
	}
	if _, ok, err := db.GetFactoryAuthorityEscalationGate(ctx, "missing"); err != nil || ok {
		t.Fatalf("missing authority gate = %v, %v", ok, err)
	}
	if duplicate, handled, err := db.CreateFactoryAuthorityEscalationGate(ctx, "session", "request", "external_directory", "/tmp", at); err != nil || !handled || duplicate.IssueID != authority.IssueID {
		t.Fatalf("duplicate authority gate = %#v, %v, %v", duplicate, handled, err)
	}
	if _, handled, err := db.CreateFactoryAuthorityEscalationGate(ctx, "session", "request-2", "bash", "rm", at); err != nil || handled {
		t.Fatalf("non-authority permission = %v, %v", handled, err)
	}
	if gate, _, err := db.ResolveFactoryAuthorityEscalationGate(ctx, authority.IssueID, "approve", at); err != nil || gate.Resolution != "approve_pending" {
		t.Fatalf("authority resolution = %#v, %v", gate, err)
	}
	if gate, err := db.CompleteFactoryAuthorityEscalationGate(ctx, authority.IssueID, "approve", at); err != nil || gate.Resolution != "approve" {
		t.Fatalf("authority delivery = %#v, %v", gate, err)
	}
	if gate, _, err := db.ResolveFactoryRecoveryGate(ctx, recovery.IssueID, "resume", "continue", at); err != nil || gate.Resolution != "resume" {
		t.Fatalf("recovery resolution = %#v, %v", gate, err)
	}
	if paused, err := db.IsFactoryAttemptRecoveryPaused(ctx, attempt.ID); err != nil || paused {
		t.Fatalf("resolved recovery paused = %v, %v", paused, err)
	}
	for _, tc := range []struct {
		workID, session, recoveryAction, authorityAction string
		index                                            int
	}{
		{workID: epic.ID + ".1.98", session: "session-2", recoveryAction: "cancel", authorityAction: "reject", index: 98},
		{workID: epic.ID + ".1.97", session: "session-3", recoveryAction: "retry", authorityAction: "", index: 97},
	} {
		if _, err := db.db.Exec(`INSERT INTO factory_issue (id, epic_id, kind, title, status, created_at) VALUES (?, ?, 'implementation', 'Work', 'open', 0)`, tc.workID, epic.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := db.db.Exec(`INSERT INTO factory_issue_hierarchy (parent_issue_id, child_issue_id, child_index, requirement) VALUES (?, ?, ?, 'required')`, molID, tc.workID, tc.index); err != nil {
			t.Fatal(err)
		}
		attempt, err := db.CreatePreparedFactoryAttempt(ctx, epic.ID, tc.workID, model.FactoryAttemptPolicy{Repository: "/repo", Profile: "factory-implement/v1"}, at)
		if err != nil {
			t.Fatal(err)
		}
		if changed, err := db.ActivateFactoryAttempt(ctx, attempt.ID, model.PlanningSession{Platform: "opencode", ID: tc.session}, at); err != nil || !changed {
			t.Fatalf("activation = %v, %v", changed, err)
		}
		recovery, err := db.CreateFactoryRecoveryGate(ctx, attempt.ID, "Choose", "blocked", nil, at)
		if err != nil {
			t.Fatal(err)
		}
		if tc.authorityAction != "" {
			authority, handled, err := db.CreateFactoryAuthorityEscalationGate(ctx, tc.session, "request-"+tc.session, "external_directory", "/tmp", at)
			if err != nil || !handled {
				t.Fatalf("authority gate = %v, %v", handled, err)
			}
			if _, _, err := db.ResolveFactoryAuthorityEscalationGate(ctx, authority.IssueID, "invalid", at); err == nil {
				t.Fatal("invalid authority action succeeded")
			}
			if gate, _, err := db.ResolveFactoryAuthorityEscalationGate(ctx, authority.IssueID, tc.authorityAction, at); err != nil || gate.Resolution != tc.authorityAction+"_pending" {
				t.Fatalf("authority resolution = %#v, %v", gate, err)
			}
			if _, err := db.CompleteFactoryAuthorityEscalationGate(ctx, authority.IssueID, tc.authorityAction, at); err != nil {
				t.Fatal(err)
			}
		}
		if tc.recoveryAction == "retry" {
			if _, _, err := db.ResolveFactoryRecoveryGate(ctx, recovery.IssueID, "invalid", "", at); err == nil {
				t.Fatal("invalid recovery action succeeded")
			}
		}
		if gate, _, err := db.ResolveFactoryRecoveryGate(ctx, recovery.IssueID, tc.recoveryAction, "continue", at); err != nil || gate.Resolution != tc.recoveryAction {
			t.Fatalf("recovery resolution = %#v, %v", gate, err)
		}
	}
}

func TestClaimFactoryImplementation(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	ctx := context.Background()
	epic, err := db.CreateFactoryEpic(ctx, "Epic", "", "/repo", "", nativeTracerFormula(t))
	if err != nil {
		t.Fatal(err)
	}
	molID := factoryIssueID(t, db, epic.ID, "mol")
	for _, title := range []string{"First", "Second"} {
		if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Action: "create", EpicID: epic.ID, ParentID: molID, Kind: "implementation", Title: title}); err != nil {
			t.Fatal(err)
		}
	}
	issues, err := db.ListFactoryIssues(ctx, epic.ID)
	if err != nil {
		t.Fatal(err)
	}
	var workIDs []string
	for _, issue := range issues {
		if issue.Kind == "implementation" {
			workIDs = append(workIDs, issue.ID)
		}
	}
	if len(workIDs) != 2 {
		t.Fatalf("implementation issues = %v", workIDs)
	}
	if _, _, err := db.ClaimFactoryImplementation(ctx, epic.ID, workIDs[0], "factory-implement/v1", time.Now()); err == nil {
		t.Fatal("claimed implementation without local execution acknowledgement")
	}
	if err := db.UpsertFactoryLocalExecutionAck(ctx, "local", "/repo", "factory-implement", "v1", "operator", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.ClaimFactoryImplementation(ctx, "missing", workIDs[0], "factory-implement/v1", time.Now()); !errors.Is(err, model.ErrNativeEpicNotFound) {
		t.Fatalf("missing Epic claim error = %v", err)
	}
	if _, _, err := db.ClaimFactoryImplementation(ctx, epic.ID, factoryIssueID(t, db, epic.ID, "plan"), "factory-implement/v1", time.Now()); err == nil {
		t.Fatal("claimed a non-implementation issue")
	}
	if err := db.SetFactoryCapacityPolicy(ctx, model.FactoryCapacityPolicy{GlobalCapacity: 2, ProjectCapacity: 2}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.ClaimFactoryImplementation(ctx, epic.ID, workIDs[0], "factory-plan/v1", time.Now()); err == nil {
		t.Fatal("implementation claim accepted a planning profile")
	}
	claimedEpic, attempt, err := db.ClaimFactoryImplementation(ctx, epic.ID, workIDs[0], "factory-implement/v1", time.UnixMilli(2_000))
	if err != nil || claimedEpic.ID != epic.ID || attempt.WorkID != workIDs[0] || attempt.Phase != model.FactoryAttemptPrepared || attempt.FrozenPolicy.Repository != "/repo" {
		t.Fatalf("claim = %#v, %#v, %v", claimedEpic, attempt, err)
	}
	if _, _, err := db.ClaimFactoryImplementation(ctx, epic.ID, workIDs[1], "factory-implement/v1", time.Now()); err == nil {
		t.Fatal("claimed parallel work in one shared Epic workspace")
	}
	if err := db.SetFactoryAttemptDeliveryTarget(ctx, attempt.ID, "github", "github.com", "acme/repo", time.Now()); err != nil {
		t.Fatal(err)
	}
	storedAttempt, found, err := db.GetFactoryAttempt(ctx, attempt.ID)
	if err != nil || !found || storedAttempt.FrozenPolicy.DeliveryRemoteRepo != "acme/repo" {
		t.Fatalf("delivery target = %#v, %v, %v", storedAttempt.FrozenPolicy, found, err)
	}
	if changed, err := db.ActivateFactoryAttempt(ctx, attempt.ID, model.PlanningSession{Platform: "opencode", ID: "session"}, time.Now()); err != nil || !changed {
		t.Fatalf("activate implementation = %v, %v", changed, err)
	}
	if owned, err := db.IsFactoryImplementationSession(ctx, "session"); err != nil || !owned {
		t.Fatalf("implementation session = %v, %v", owned, err)
	}
	if owned, err := db.IsFactoryImplementationSession(ctx, "other"); err != nil || owned {
		t.Fatalf("unrelated session = %v, %v", owned, err)
	}
	if changed, err := db.CompleteFactoryImplementationAttempt(ctx, attempt.ID, "wrong", model.FactoryAttemptResult{SchemaVersion: 1, Summary: "done"}, time.Now()); err != nil || changed {
		t.Fatalf("completion with wrong token = %v, %v", changed, err)
	}
	if changed, err := db.StopFactoryAttempt(ctx, attempt.ID, time.Now()); err != nil || !changed {
		t.Fatalf("stop implementation = %v, %v", changed, err)
	}
	if changed, err := db.CompleteFactoryImplementationAttempt(ctx, attempt.ID, attempt.AgentToken, model.FactoryAttemptResult{SchemaVersion: 1, Summary: "done", PRURL: "https://forge.example/pr/1"}, time.Now()); err != nil || !changed {
		t.Fatalf("complete implementation = %v, %v", changed, err)
	}
	issues, err = db.ListFactoryIssues(ctx, epic.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, issue := range issues {
		if issue.ID == workIDs[0] && (issue.Status != "closed" || issue.Outcome != "succeeded") {
			t.Fatalf("completed issue = %#v", issue)
		}
		if issue.ID == workIDs[1] && issue.Status != "open" {
			t.Fatalf("rejected issue status = %q", issue.Status)
		}
	}
	_, second, err := db.ClaimFactoryImplementation(ctx, epic.ID, workIDs[1], "factory-implement/v1", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetFactoryAttemptDeliveryTarget(ctx, second.ID, "forgejo", "evil.example", "attacker/repo", time.Now()); err != nil {
		t.Fatal(err)
	}
	storedSecond, found, err := db.GetFactoryAttempt(ctx, second.ID)
	if err != nil || !found || storedSecond.FrozenPolicy.DeliveryRemoteRepo != "acme/repo" {
		t.Fatalf("reused delivery target = %#v, %v, %v", storedSecond.FrozenPolicy, found, err)
	}
	if changed, err := db.ActivateFactoryAttempt(ctx, second.ID, model.PlanningSession{Platform: "opencode", ID: "session-2"}, time.Now()); err != nil || !changed {
		t.Fatalf("activate second implementation = %v, %v", changed, err)
	}
	if changed, err := db.StopFactoryAttempt(ctx, second.ID, time.Now()); err != nil || !changed {
		t.Fatalf("stop second implementation = %v, %v", changed, err)
	}
	if changed, err := db.CompleteFactoryImplementationAttempt(ctx, second.ID, second.AgentToken, model.FactoryAttemptResult{SchemaVersion: 1, Summary: "done", PRURL: "https://forge.example/pr/2"}, time.Now()); err == nil || changed {
		t.Fatalf("accepted another PR for one Epic = %v, %v", changed, err)
	}
	if changed, err := db.CompleteFactoryImplementationAttempt(ctx, second.ID, second.AgentToken, model.FactoryAttemptResult{SchemaVersion: 1, Summary: "done", PRURL: "https://forge.example/pr/1"}, time.Now()); err != nil || !changed {
		t.Fatalf("complete second implementation = %v, %v", changed, err)
	}
	if _, err := db.db.Exec(`UPDATE factory_epic SET status = 'closed' WHERE id = ?`, epic.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.ClaimFactoryImplementation(ctx, epic.ID, workIDs[1], "factory-implement/v1", time.Now()); err == nil {
		t.Fatal("claimed implementation from closed Epic")
	}
}

func TestFactoryAttemptTokenAndFailureTransitions(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	ctx := context.Background()
	now := time.Now()
	epic, err := db.CreateFactoryEpic(ctx, "Epic", "", "/repo", "", nativeTracerFormula(t))
	if err != nil {
		t.Fatal(err)
	}
	molID := factoryIssueID(t, db, epic.ID, "mol")
	if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Action: "create", EpicID: epic.ID, ParentID: molID, Kind: "implementation", Title: "Work"}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertFactoryLocalExecutionAck(ctx, "local", "/repo", "factory-implement", "v1", "operator", now); err != nil {
		t.Fatal(err)
	}
	issues, err := db.ListFactoryIssues(ctx, epic.ID)
	if err != nil {
		t.Fatal(err)
	}
	var workID string
	for _, issue := range issues {
		if issue.Kind == "implementation" {
			workID = issue.ID
		}
	}
	_, attempt, err := db.ClaimFactoryImplementation(ctx, epic.ID, workID, "factory-implement/v1", now)
	if err != nil {
		t.Fatal(err)
	}
	if valid, err := db.ValidateFactoryAttemptToken(ctx, attempt.ID, attempt.AgentToken); err != nil || !valid {
		t.Fatalf("valid token = %v, %v", valid, err)
	}
	if valid, err := db.ValidateFactoryAttemptToken(ctx, attempt.ID, "wrong"); err != nil || valid {
		t.Fatalf("invalid token = %v, %v", valid, err)
	}
	if changed, err := db.ActivateFactoryAttempt(ctx, attempt.ID, model.PlanningSession{Platform: "opencode", ID: "session"}, now); err != nil || !changed {
		t.Fatalf("activate = %v, %v", changed, err)
	}
	if changed, err := db.FailFactoryAttempt(ctx, attempt.ID, model.FactoryAttemptFailure{Type: "interrupted_startup", Message: "session disappeared"}, now); err != nil || !changed {
		t.Fatalf("interrupted failure = %v, %v", changed, err)
	}
	issues, err = db.ListFactoryIssues(ctx, epic.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, issue := range issues {
		if issue.ID == workID && (issue.Status != "retry_wait" || issue.RetryAttempts != 1) {
			t.Fatalf("retried implementation = %#v", issue)
		}
	}

	planID := factoryIssueID(t, db, epic.ID, "plan")
	_, planAttempt, err := db.ClaimFactoryPlan(ctx, epic.ID, planID, "factory-plan/v1", now)
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := db.ActivateFactoryAttempt(ctx, planAttempt.ID, model.PlanningSession{Platform: "opencode", ID: "plan"}, now); err != nil || !changed {
		t.Fatalf("activate plan = %v, %v", changed, err)
	}
	if changed, err := db.FailFactoryAttempt(ctx, planAttempt.ID, model.FactoryAttemptFailure{Type: "prompt_failed", Message: "prompt unavailable"}, now); err != nil || !changed {
		t.Fatalf("prompt failure = %v, %v", changed, err)
	}
	issues, err = db.ListFactoryIssues(ctx, epic.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, issue := range issues {
		if issue.ID == planID && issue.Status != "open" {
			t.Fatalf("reopened plan = %#v", issue)
		}
	}
}

// issueByID is a test helper that fails when the issue is missing.
func issueByID(t *testing.T, db *DB, epicID, issueID string) model.NativeIssue {
	t.Helper()
	issues, err := db.ListFactoryIssues(context.Background(), epicID)
	if err != nil {
		t.Fatal(err)
	}
	for _, issue := range issues {
		if issue.ID == issueID {
			return issue
		}
	}
	t.Fatalf("issue %q not found", issueID)
	return model.NativeIssue{}
}

// claimAndFail is a test helper: claim, activate, and fail one attempt for workID.
func claimAndFail(t *testing.T, db *DB, epicID, workID string, at time.Time) {
	t.Helper()
	ctx := context.Background()
	if err := db.WakeFactoryRetries(ctx, at.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	_, attempt, err := db.ClaimFactoryImplementation(ctx, epicID, workID, "factory-implement/v1", at)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.FailFactoryAttempt(ctx, attempt.ID, model.FactoryAttemptFailure{Type: "launch_failed", Message: "no session"}, at); err != nil {
		t.Fatal(err)
	}
}

func TestFactoryLaunchRetriesBackOffThenReopen(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	ctx := context.Background()
	now := time.Now()
	epic, err := db.CreateFactoryEpic(ctx, "Epic", "", "/repo", "", nativeTracerFormula(t))
	if err != nil {
		t.Fatal(err)
	}
	molID := factoryIssueID(t, db, epic.ID, "mol")
	if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Action: "create", EpicID: epic.ID, ParentID: molID, Kind: "task", Title: "Work"}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertFactoryLocalExecutionAck(ctx, "local", "/repo", "factory-implement", "v1", "operator", now); err != nil {
		t.Fatal(err)
	}
	workID := factoryIssueID(t, db, epic.ID, "task")
	if err := db.ReopenFactoryIssue(ctx, epic.ID, workID); err == nil {
		t.Fatal("reopening open work succeeded")
	}
	for i, wait := range factoryRetryBackoff {
		claimAndFail(t, db, epic.ID, workID, now)
		issue := issueByID(t, db, epic.ID, workID)
		if issue.Status != "retry_wait" || issue.RetryAttempts != i+1 || issue.RetryAt != now.Add(wait).UnixMilli() {
			t.Fatalf("attempt %d: %#v, want retry_wait at +%s", i+1, issue, wait)
		}
	}
	claimAndFail(t, db, epic.ID, workID, now)
	issue := issueByID(t, db, epic.ID, workID)
	if issue.Status != "closed" || issue.Outcome != "failed" || issue.RetryAttempts != len(factoryRetryBackoff)+1 {
		t.Fatalf("exhausted work = %#v", issue)
	}
	if err := db.ReopenFactoryIssue(ctx, epic.ID, workID); err != nil {
		t.Fatal(err)
	}
	issue = issueByID(t, db, epic.ID, workID)
	if issue.Status != "open" || issue.Outcome != "" || issue.OutcomeReason != "" || issue.RetryAttempts != 0 || issue.DispatchState != "ready" {
		t.Fatalf("reopened work = %#v", issue)
	}
	if err := db.ReopenFactoryIssue(ctx, epic.ID, factoryIssueID(t, db, epic.ID, "plan")); err == nil {
		t.Fatal("reopening a plan succeeded")
	}
}

func TestPlanApprovalClosesPlanAndHandBuiltMaterialization(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	ctx := context.Background()
	now := time.Now()
	epic, err := db.CreateFactoryEpic(ctx, "Epic", "", "/repo", "", nativeTracerFormula(t))
	if err != nil {
		t.Fatal(err)
	}
	molID := factoryIssueID(t, db, epic.ID, "mol")
	planID := factoryIssueID(t, db, epic.ID, "plan")
	materializationID := factoryIssueID(t, db, epic.ID, "materialization")
	_, planAttempt, err := db.ClaimFactoryPlan(ctx, epic.ID, planID, "factory-plan/v1", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ActivateFactoryAttempt(ctx, planAttempt.ID, model.PlanningSession{Platform: "opencode", ID: "plan"}, now); err != nil {
		t.Fatal(err)
	}
	// Tasks added before approval must not satisfy the materialization yet.
	if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Action: "create", EpicID: epic.ID, ParentID: molID, Kind: "task", Title: "Early"}); err != nil {
		t.Fatal(err)
	}
	if got := issueByID(t, db, epic.ID, materializationID); got.Status != "open" {
		t.Fatalf("materialization before approval = %#v", got)
	}
	manifest := `{"epicId":"` + epic.ID + `","molId":"` + molID + `","project":"/repo","nodes":[{"key":"implement","type":"implementation","requirement":"required"}]}`
	proposal, err := db.SaveFactoryProposalRevision(ctx, model.NativeProposalRevision{EpicID: epic.ID, MolID: molID, Project: "/repo", ManifestJSON: manifest, ContentHash: "h1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.DecideFactoryPlanGate(ctx, epic.ID, "approve", proposal.Revision, proposal.ContentHash, ""); err != nil {
		t.Fatal(err)
	}
	if got := issueByID(t, db, epic.ID, planID); got.Status != "closed" || got.Outcome != "succeeded" {
		t.Fatalf("approved plan = %#v", got)
	}
	if got := issueByID(t, db, epic.ID, materializationID); got.Status != "closed" || got.Outcome != "succeeded" {
		t.Fatalf("hand-built materialization = %#v", got)
	}
	attempts, err := db.ListFactoryAttempts(ctx, epic.ID)
	if err != nil || len(attempts) != 1 || attempts[0].Phase != model.FactoryAttemptTerminal || attempts[0].Outcome != "succeeded" {
		t.Fatalf("plan attempt after approval = %#v, %v", attempts, err)
	}
}

func TestMutateGraphCreateSatisfiesApprovedMaterialization(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	ctx := context.Background()
	epic, err := db.CreateFactoryEpic(ctx, "Epic", "", "/repo", "", nativeTracerFormula(t))
	if err != nil {
		t.Fatal(err)
	}
	molID := factoryIssueID(t, db, epic.ID, "mol")
	materializationID := factoryIssueID(t, db, epic.ID, "materialization")
	manifest := `{"epicId":"` + epic.ID + `","molId":"` + molID + `","project":"/repo","nodes":[{"key":"implement","type":"implementation","requirement":"required"}]}`
	proposal, err := db.SaveFactoryProposalRevision(ctx, model.NativeProposalRevision{EpicID: epic.ID, MolID: molID, Project: "/repo", ManifestJSON: manifest, ContentHash: "h1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.DecideFactoryPlanGate(ctx, epic.ID, "approve", proposal.Revision, proposal.ContentHash, ""); err != nil {
		t.Fatal(err)
	}
	// No executable work yet: Materialize is still the expected next step.
	if got := issueByID(t, db, epic.ID, materializationID); got.Status != "open" {
		t.Fatalf("materialization without work = %#v", got)
	}
	if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Action: "create", EpicID: epic.ID, ParentID: molID, Kind: "mol", Title: "Container"}); err != nil {
		t.Fatal(err)
	}
	if got := issueByID(t, db, epic.ID, materializationID); got.Status != "open" {
		t.Fatalf("materialization after nested mol = %#v", got)
	}
	if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Action: "create", EpicID: epic.ID, ParentID: molID, Kind: "task", Title: "Hand-built"}); err != nil {
		t.Fatal(err)
	}
	if got := issueByID(t, db, epic.ID, materializationID); got.Status != "closed" || got.Outcome != "succeeded" {
		t.Fatalf("materialization after task = %#v", got)
	}
	if _, err := db.MaterializeFactoryPlan(ctx, epic.ID, materializationID, "factory-materialize/v1", time.Now()); err == nil {
		t.Fatal("materializing a hand-built graph succeeded")
	}
}

func TestMigrateToV68RepairsApprovedEpics(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	ctx := context.Background()
	now := time.Now()
	epic, err := db.CreateFactoryEpic(ctx, "Epic", "", "/repo", "", nativeTracerFormula(t))
	if err != nil {
		t.Fatal(err)
	}
	molID := factoryIssueID(t, db, epic.ID, "mol")
	planID := factoryIssueID(t, db, epic.ID, "plan")
	materializationID := factoryIssueID(t, db, epic.ID, "materialization")
	if _, _, err := db.ClaimFactoryPlan(ctx, epic.ID, planID, "factory-plan/v1", now); err != nil {
		t.Fatal(err)
	}
	if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Action: "create", EpicID: epic.ID, ParentID: molID, Kind: "task", Title: "Work"}); err != nil {
		t.Fatal(err)
	}
	// Recreate the pre-v68 state: approved gate, plan still in progress, materialization open.
	if _, err := db.db.ExecContext(ctx, `INSERT INTO factory_plan_gate (epic_id, issue_id, proposal_revision, proposal_hash, outcome, resolution, updated_at) VALUES (?, ?, 1, 'h', 'succeeded', 'approved', 0)`, epic.ID, factoryIssueID(t, db, epic.ID, "gate")); err != nil {
		t.Fatal(err)
	}
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrateToV68(tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if got := issueByID(t, db, epic.ID, planID); got.Status != "closed" || got.Outcome != "succeeded" {
		t.Fatalf("repaired plan = %#v", got)
	}
	if got := issueByID(t, db, epic.ID, materializationID); got.Status != "closed" || got.Outcome != "succeeded" {
		t.Fatalf("repaired materialization = %#v", got)
	}
	attempts, err := db.ListFactoryAttempts(ctx, epic.ID)
	if err != nil || len(attempts) != 1 || attempts[0].Phase != model.FactoryAttemptTerminal {
		t.Fatalf("repaired plan attempt = %#v, %v", attempts, err)
	}
}
