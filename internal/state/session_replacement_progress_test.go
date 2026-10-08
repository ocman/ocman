package state

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestReplacementCheckpointPublishIsAtomicAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	database, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	baseline := map[string]SessionInterruption{"a": {MessageID: "user", BaselineStatus: "busy"}}
	if err := database.PrepareSessionInterruptions(t.Context(), "opencode", "/root", baseline); err != nil {
		t.Fatal(err)
	}
	snapshot := ReplacementStopSnapshot{Baseline: baseline, StartedAt: 100, AdmissionStartedAt: 50, Membership: map[string]bool{"/repo": true}, Candidates: []ReplacementCandidate{{SessionID: "a", Directory: "/repo"}, {SessionID: "b", Directory: "/repo"}}}
	if err := database.BeginSessionReplacementStopSnapshot(t.Context(), "opencode", "/root", snapshot); err != nil {
		t.Fatal(err)
	}
	if err := database.MarkReplacementStopped(t.Context(), "opencode", "/root"); err != nil {
		t.Fatal(err)
	}
	if err := database.InitializeReplacementReconciliation(t.Context(), "opencode", "/root", snapshot.Candidates); err != nil {
		t.Fatal(err)
	}
	batch, err := database.ReplacementReconciliationBatch(t.Context(), "opencode", "/root", 1)
	if err != nil || len(batch) != 1 || batch[0].SessionID != "a" || batch[0].Member == nil || !*batch[0].Member {
		t.Fatalf("batch=%v err=%v", batch, err)
	}
	first := SessionInterruption{MessageID: "assistant", ObservedAt: 101, Message: "original cause"}
	if err := database.CheckpointReplacementCandidate(t.Context(), "opencode", "/root", batch[0], true, first); err != nil {
		t.Fatal(err)
	}
	if _, err := database.PublishReplacementReconciliation(t.Context(), "opencode", "/root"); !errors.Is(err, ErrReplacementReconciliationPending) {
		t.Fatalf("partial publication accepted: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	database, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	progress, err := database.ReplacementReconciliationStatus(t.Context(), "opencode", "/root")
	if err != nil || !progress.Initialized || progress.AdmissionStartedAt != 50 {
		t.Fatalf("checkpoint metadata lost: %+v %v", progress, err)
	}
	batch, err = database.ReplacementReconciliationBatch(t.Context(), "opencode", "/root", 10)
	if err != nil || len(batch) != 1 || batch[0].SessionID != "b" {
		t.Fatalf("processed read restarted: %v %v", batch, err)
	}
	if err := database.CheckpointReplacementCandidate(t.Context(), "opencode", "/root", batch[0], true, SessionInterruption{}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.Exec(`CREATE TRIGGER reject_progress_publication BEFORE INSERT ON session_interruption BEGIN SELECT RAISE(ABORT,'publication failed'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.PublishReplacementReconciliation(t.Context(), "opencode", "/root"); err == nil {
		t.Fatal("publication ignored storage failure")
	}
	if got, err := database.SessionInterruptions(t.Context(), "opencode", "a"); err != nil || len(got) != 0 {
		t.Fatalf("failed publication leaked history: %v %v", got, err)
	}
	if _, err := database.db.Exec(`DROP TRIGGER reject_progress_publication`); err != nil {
		t.Fatal(err)
	}
	ids, err := database.PublishReplacementReconciliation(t.Context(), "opencode", "/root")
	if err != nil || len(ids) != 1 || ids[0] != "a" {
		t.Fatalf("publication=%v %v", ids, err)
	}
	if got, err := database.SessionInterruptions(t.Context(), "opencode", "a"); err != nil || len(got) != 1 || got[0] != first {
		t.Fatalf("cause changed on retry: %v %v", got, err)
	}
	if got, err := database.SessionInterruptions(t.Context(), "opencode", "b"); err != nil || len(got) != 0 {
		t.Fatalf("no-notice candidate got history: %v %v", got, err)
	}
}

func TestReplacementProgressStorageCancellationAndUnknownRoot(t *testing.T) {
	database := openTestStateDB(t)
	defer database.Close()
	progress, err := database.ReplacementReconciliationStatus(t.Context(), "opencode", "/missing")
	if err != nil || progress.Initialized || progress.Seeded {
		t.Fatalf("unknown root had progress: %+v %v", progress, err)
	}
	if _, err := database.PublishReplacementReconciliation(t.Context(), "opencode", "/missing"); !errors.Is(err, ErrReplacementReconciliationPending) {
		t.Fatalf("unknown root authorized publication: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	checks := []func() error{
		func() error { _, err := database.ReplacementReconciliationStatus(ctx, "opencode", "/root"); return err },
		func() error { return database.InitializeReplacementReconciliation(ctx, "opencode", "/root", nil) },
		func() error {
			_, err := database.ReplacementReconciliationBatch(ctx, "opencode", "/root", 64)
			return err
		},
		func() error {
			return database.CheckpointReplacementCandidate(ctx, "opencode", "/root", ReplacementCandidate{SessionID: "s", Directory: "/repo"}, true, SessionInterruption{})
		},
		func() error { return database.CheckpointReplacementMembership(ctx, "opencode", "/root", "/repo", true) },
		func() error {
			_, err := database.PublishReplacementReconciliation(ctx, "opencode", "/root")
			return err
		},
	}
	for i, check := range checks {
		if err := check(); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled storage operation %d succeeded: %v", i, err)
		}
	}
}

func TestReplacementProgressInitializationFailurePreservesSnapshot(t *testing.T) {
	database := openTestStateDB(t)
	defer database.Close()
	if err := database.PrepareSessionInterruptions(t.Context(), "opencode", "/root", nil); err != nil {
		t.Fatal(err)
	}
	if err := database.BeginSessionReplacementStop(t.Context(), "opencode", "/root", nil, 100); err != nil {
		t.Fatal(err)
	}
	if err := database.MarkReplacementStopped(t.Context(), "opencode", "/root"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.Exec(`CREATE TRIGGER reject_candidate BEFORE INSERT ON session_replacement_candidate BEGIN SELECT RAISE(ABORT,'candidate storage unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	if err := database.InitializeReplacementReconciliation(t.Context(), "opencode", "/root", []ReplacementCandidate{{SessionID: "s", Directory: "/repo"}}); err == nil {
		t.Fatal("initialization ignored storage failure")
	}
	progress, err := database.ReplacementReconciliationStatus(t.Context(), "opencode", "/root")
	if err != nil || progress.Initialized || progress.StopStartedAt != 100 {
		t.Fatalf("initialization partially committed: %+v %v", progress, err)
	}
	if _, err := database.db.Exec(`DROP TRIGGER reject_candidate`); err != nil {
		t.Fatal(err)
	}
	if err := database.InitializeReplacementReconciliation(t.Context(), "opencode", "/root", []ReplacementCandidate{{SessionID: "s", Directory: "/repo"}}); err != nil {
		t.Fatal(err)
	}
	// Repeating initialization cannot append work to the frozen stopped view.
	if err := database.InitializeReplacementReconciliation(t.Context(), "opencode", "/root", []ReplacementCandidate{{SessionID: "late", Directory: "/repo"}}); err != nil {
		t.Fatal(err)
	}
	batch, err := database.ReplacementReconciliationBatch(t.Context(), "opencode", "/root", 64)
	if err != nil || len(batch) != 1 || batch[0].SessionID != "s" {
		t.Fatalf("initialization changed frozen work: %v %v", batch, err)
	}
	if _, err := database.db.Exec(`UPDATE session_replacement_candidate SET prior_json='invalid JSON'`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ReplacementReconciliationBatch(t.Context(), "opencode", "/root", 64); err == nil {
		t.Fatal("corrupt original evidence was treated as unsampled")
	}
}

func TestReplacementMembershipWriteFailurePreventsStopBoundary(t *testing.T) {
	database := openTestStateDB(t)
	defer database.Close()
	if err := database.PrepareSessionInterruptions(t.Context(), "opencode", "/root", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.Exec(`CREATE TRIGGER reject_membership BEFORE INSERT ON session_replacement_directory BEGIN SELECT RAISE(ABORT,'membership storage unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	if err := database.BeginSessionReplacementStopSnapshot(t.Context(), "opencode", "/root", ReplacementStopSnapshot{StartedAt: 100, AdmissionStartedAt: 50, Membership: map[string]bool{"/repo": true}}); err == nil {
		t.Fatal("stop proceeded without durable membership")
	}
	at, _, err := database.SessionReplacementStopEvidence(t.Context(), "opencode", "/root")
	if err != nil || at != 0 {
		t.Fatalf("failed membership write left a stop boundary: %d %v", at, err)
	}
}

func TestReplacementCheckpointFailureRollsBackMembershipAndResult(t *testing.T) {
	database := openTestStateDB(t)
	defer database.Close()
	if err := database.PrepareSessionInterruptions(t.Context(), "opencode", "/root", nil); err != nil {
		t.Fatal(err)
	}
	if err := database.BeginSessionReplacementStop(t.Context(), "opencode", "/root", nil, 100); err != nil {
		t.Fatal(err)
	}
	if err := database.MarkReplacementStopped(t.Context(), "opencode", "/root"); err != nil {
		t.Fatal(err)
	}
	if err := database.InitializeReplacementReconciliation(t.Context(), "opencode", "/root", []ReplacementCandidate{{SessionID: "a", Directory: "/unknown"}}); err != nil {
		t.Fatal(err)
	}
	batch, err := database.ReplacementReconciliationBatch(t.Context(), "opencode", "/root", 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.Exec(`CREATE TRIGGER reject_checkpoint BEFORE UPDATE ON session_replacement_candidate BEGIN SELECT RAISE(ABORT,'checkpoint failed'); END`); err != nil {
		t.Fatal(err)
	}
	if err := database.CheckpointReplacementCandidate(t.Context(), "opencode", "/root", batch[0], true, SessionInterruption{MessageID: "m"}); err == nil {
		t.Fatal("checkpoint failure ignored")
	}
	batch, err = database.ReplacementReconciliationBatch(t.Context(), "opencode", "/root", 1)
	if err != nil || len(batch) != 1 || batch[0].Member != nil {
		t.Fatalf("partial checkpoint persisted: %v %v", batch, err)
	}
	if other, err := database.ReplacementReconciliationBatch(t.Context(), "another-owner", "/root", 10); err != nil || len(other) != 0 {
		t.Fatalf("checkpoint crossed owner: %v %v", other, err)
	}
}

func TestReplacementCheckpointBatchRollsBackOnSecondFailure(t *testing.T) {
	database := openTestStateDB(t)
	defer database.Close()
	if err := database.PrepareSessionInterruptions(t.Context(), "opencode", "/root", nil); err != nil {
		t.Fatal(err)
	}
	if err := database.BeginSessionReplacementStop(t.Context(), "opencode", "/root", nil, 100); err != nil {
		t.Fatal(err)
	}
	if err := database.MarkReplacementStopped(t.Context(), "opencode", "/root"); err != nil {
		t.Fatal(err)
	}
	candidates := []ReplacementCandidate{{SessionID: "a", Directory: "/repo"}, {SessionID: "b", Directory: "/repo"}}
	if err := database.InitializeReplacementReconciliation(t.Context(), "opencode", "/root", candidates); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.Exec(`CREATE TRIGGER reject_second_checkpoint BEFORE UPDATE ON session_replacement_candidate WHEN NEW.session_id='b' BEGIN SELECT RAISE(ABORT,'checkpoint failed'); END`); err != nil {
		t.Fatal(err)
	}
	results := []ReplacementCheckpoint{{Candidate: candidates[0], Member: true}, {Candidate: candidates[1], Member: true}}
	if err := database.CheckpointReplacementBatch(t.Context(), "opencode", "/root", results); err == nil {
		t.Fatal("batch checkpoint failure ignored")
	}
	batch, err := database.ReplacementReconciliationBatch(t.Context(), "opencode", "/root", 2)
	if err != nil || len(batch) != 2 || batch[0].Member != nil || batch[1].Member != nil {
		t.Fatalf("partial batch checkpoint persisted: %v %v", batch, err)
	}
	if _, err := database.db.Exec(`DROP TRIGGER reject_second_checkpoint`); err != nil {
		t.Fatal(err)
	}
	if err := database.CheckpointReplacementBatch(t.Context(), "opencode", "/root", results); err != nil {
		t.Fatal(err)
	}
	if batch, err := database.ReplacementReconciliationBatch(t.Context(), "opencode", "/root", 2); err != nil || len(batch) != 0 {
		t.Fatalf("batch did not drain: %v %v", batch, err)
	}
}
