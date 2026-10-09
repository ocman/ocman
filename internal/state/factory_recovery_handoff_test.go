package state

import (
	"errors"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory/model"
)

func TestRecoveryCheckpointYieldsWorkspaceWithoutCompletingIssue(t *testing.T) {
	db := openTestStateDB(t)
	t.Cleanup(func() { _ = db.Close() })
	ctx, now := t.Context(), time.Now()
	epic, err := db.CreateFactoryEpic(ctx, "", "Recovery handoff", "", "/repo", "", nativeTracerFormula(t))
	if err != nil {
		t.Fatal(err)
	}
	parent := factoryIssueID(t, db, epic.ID, "mol")
	for _, title := range []string{"Acceptance", "Prerequisite"} {
		if err := db.MutateFactoryGraph(ctx, model.GraphMutation{Action: "create", EpicID: epic.ID, ParentID: parent, Kind: "implementation", Title: title}); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.UpsertFactoryLocalExecutionAck(ctx, "local", "/repo", "factory-implement", "v1", "operator", now); err != nil {
		t.Fatal(err)
	}
	acceptanceID, prerequisiteID := issueIDWithTitle(t, db, epic.ID, "Acceptance"), issueIDWithTitle(t, db, epic.ID, "Prerequisite")
	_, acceptance, err := db.ClaimFactoryImplementation(ctx, epic.ID, acceptanceID, "factory-implement/v1", now)
	if err != nil {
		t.Fatal(err)
	}
	acceptance.FrozenPolicy.Branch = "factory/" + epic.ID
	acceptance.FrozenPolicy.TargetBranch = "main"
	acceptance.FrozenPolicy.CheckpointSHA = "prior-checkpoint"
	if err := db.SetFactoryAttemptWorkspace(ctx, acceptance.ID, acceptance.FrozenPolicy); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ActivateFactoryAttempt(ctx, acceptance.ID, model.PlanningSession{Platform: "opencode", ID: "acceptance-session"}, now); err != nil {
		t.Fatal(err)
	}
	gate, err := db.CreateFactoryRecoveryGate(ctx, acceptance.ID, "Run prerequisites first", "Blocked acceptance", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.ClaimFactoryImplementation(ctx, epic.ID, prerequisiteID, "factory-implement/v1", now); err == nil {
		t.Fatal("unverified paused work released its workspace")
	}
	// The service must first stop the writer and validate a clean, pushed HEAD.
	checkpoint := model.FactoryAttemptResult{SchemaVersion: 2, Summary: "Recovery workspace checkpoint " + gate.IssueID, Branch: acceptance.FrozenPolicy.Branch, CommitSHA: "paused-checkpoint"}
	if err := db.RecordFactoryRecoveryCheckpoint(ctx, gate.IssueID, checkpoint, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	_, prerequisite, err := db.ClaimFactoryImplementation(ctx, epic.ID, prerequisiteID, "factory-implement/v1", now.Add(2*time.Second))
	if err != nil {
		t.Fatalf("ready prerequisite blocked by checkpointed recovery: %v", err)
	}
	if prerequisite.FrozenPolicy.CheckpointSHA != checkpoint.CommitSHA {
		t.Fatalf("lost paused checkpoint: %#v", prerequisite.FrozenPolicy)
	}
	if _, _, err := db.ResolveFactoryRecoveryGate(ctx, gate.IssueID, "resume", "Continue", now); !errors.Is(err, model.ErrRecoveryWorkspaceBusy) {
		t.Fatalf("expected actionable workspace conflict, got %v", err)
	}
	// The service queues that decision; the gate stays open so the writer stays yielded.
	if err := db.QueueFactoryRecoveryResume(ctx, gate.IssueID, "Continue", now); err != nil {
		t.Fatal(err)
	}
	if queued, err := db.ListFactoryQueuedRecoveryResumes(ctx); err != nil || len(queued) != 1 || queued[0].IssueID != gate.IssueID || queued[0].Response != "Continue" {
		t.Fatalf("queued resumes = %#v, %v", queued, err)
	}
	stored, found, err := db.GetFactoryAttempt(ctx, acceptance.ID)
	if err != nil || !found || stored.Phase != model.FactoryAttemptActive || stored.Outcome != "" {
		t.Fatalf("acceptance was completed by yielding: %#v, %v", stored, err)
	}
	if changed, err := db.ActivateFactoryAttempt(ctx, prerequisite.ID, model.PlanningSession{Platform: "opencode", ID: "prerequisite-session"}, now.Add(2*time.Second)); err != nil || !changed {
		t.Fatalf("activate prerequisite: %v, %v", changed, err)
	}
	if changed, err := db.CompleteFactoryAttempt(ctx, prerequisite.ID, model.FactoryAttemptResult{SchemaVersion: 2, Summary: "Prerequisite fixed", CommitSHA: "prerequisite-checkpoint", Branch: checkpoint.Branch}, now.Add(3*time.Second)); err != nil || !changed {
		t.Fatalf("complete prerequisite: %v, %v", changed, err)
	}
	if _, resumed, err := db.ResolveFactoryRecoveryGate(ctx, gate.IssueID, "resume", "Continue", now.Add(4*time.Second)); err != nil || resumed.FrozenPolicy.CheckpointSHA != "prerequisite-checkpoint" {
		t.Fatalf("resume after workspace release: %v", err)
	}
	if queued, err := db.ListFactoryQueuedRecoveryResumes(ctx); err != nil || len(queued) != 0 {
		t.Fatalf("resolved gate still queued: %#v, %v", queued, err)
	}
	if err := db.QueueFactoryRecoveryResume(ctx, gate.IssueID, "Continue", now); err == nil {
		t.Fatal("queued a resume on a gate that is no longer open")
	}
}
