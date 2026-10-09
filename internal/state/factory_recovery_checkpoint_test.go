package state

import (
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory/model"
)

func TestRecoveryCheckpointSurvivesRetryAndCancel(t *testing.T) {
	for _, action := range []string{"retry", "cancel"} {
		t.Run(action, func(t *testing.T) {
			db := openTestStateDB(t)
			t.Cleanup(func() { _ = db.Close() })
			ctx, now := t.Context(), time.Now()
			epic, err := db.CreateFactoryEpic(ctx, "", "Retain recovery", "", "/repo", "", nativeTracerFormula(t))
			if err != nil {
				t.Fatal(err)
			}
			parent := factoryIssueID(t, db, epic.ID, "mol")
			for _, title := range []string{"Paused", "Next"} {
				if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Action: "create", EpicID: epic.ID, ParentID: parent, Kind: "implementation", Title: title}); err != nil {
					t.Fatal(err)
				}
			}
			if err := db.UpsertFactoryLocalExecutionAck(ctx, "local", "/repo", "factory-implement", "v1", "operator", now); err != nil {
				t.Fatal(err)
			}
			_, attempt, err := db.ClaimFactoryImplementation(ctx, epic.ID, issueIDWithTitle(t, db, epic.ID, "Paused"), "factory-implement/v1", now)
			if err != nil {
				t.Fatal(err)
			}
			attempt.FrozenPolicy.Branch, attempt.FrozenPolicy.TargetBranch = "factory/"+epic.ID, "main"
			if err := db.SetFactoryAttemptWorkspace(ctx, attempt.ID, attempt.FrozenPolicy); err != nil {
				t.Fatal(err)
			}
			if _, err := db.ActivateFactoryAttempt(ctx, attempt.ID, model.PlanningSession{Platform: "opencode", ID: "paused-session"}, now); err != nil {
				t.Fatal(err)
			}
			gate, err := db.CreateFactoryRecoveryGate(ctx, attempt.ID, "Prerequisites", "Blocked", nil, now)
			if err != nil {
				t.Fatal(err)
			}
			checkpoint := model.FactoryAttemptResult{Branch: attempt.FrozenPolicy.Branch, CommitSHA: "recovery-head"}
			if err := db.RecordFactoryRecoveryCheckpoint(ctx, gate.IssueID, model.FactoryAttemptResult{}, now); err == nil {
				t.Fatal("accepted empty checkpoint")
			}
			if err := db.RecordFactoryRecoveryCheckpoint(ctx, gate.IssueID, model.FactoryAttemptResult{Branch: "wrong", CommitSHA: "wrong"}, now); err == nil {
				t.Fatal("accepted another branch")
			}
			if err := db.RecordFactoryRecoveryCheckpoint(ctx, gate.IssueID, checkpoint, now); err != nil {
				t.Fatal(err)
			}
			if _, _, err := db.ResolveFactoryRecoveryGate(ctx, gate.IssueID, action, "Leave checkpoint intact", now.Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			if err := db.RecordFactoryRecoveryCheckpoint(ctx, gate.IssueID, checkpoint, now); err == nil {
				t.Fatal("updated a resolved recovery checkpoint")
			}
			_, next, err := db.ClaimFactoryImplementation(ctx, epic.ID, issueIDWithTitle(t, db, epic.ID, "Next"), "factory-implement/v1", now.Add(2*time.Second))
			if err != nil {
				t.Fatal(err)
			}
			if next.FrozenPolicy.CheckpointSHA != "recovery-head" {
				t.Fatalf("discarded %s checkpoint: %#v", action, next.FrozenPolicy)
			}
		})
	}
}
