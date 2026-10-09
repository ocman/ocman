package factory

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory/model"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
)

type recoveryCompletionStore struct {
	*state.DB
	fail bool
}

func (s *recoveryCompletionStore) CompleteFactoryRecoveryGate(ctx context.Context, id string, at time.Time) (model.RecoveryGate, error) {
	if s.fail {
		s.fail = false
		return model.RecoveryGate{}, errors.New("completion failed")
	}
	return s.DB.CompleteFactoryRecoveryGate(ctx, id, at)
}

type recoveryDeliveryLauncher struct {
	fakeImplementationLauncher
	delivered bool
	expire    bool
}

func (l *recoveryDeliveryLauncher) RecoveryResponseDelivered(context.Context, PlanningSession, string) (bool, error) {
	return l.delivered, nil
}

func (l *recoveryDeliveryLauncher) ResumeImplementationSession(ctx context.Context, session PlanningSession, gate, response string) error {
	if l.expire {
		<-ctx.Done() // The upstream accepted the prompt, but confirmation outlived the send deadline.
		return nil
	}
	return l.fakeImplementationLauncher.ResumeImplementationSession(ctx, session, gate, response)
}

func recoveryDeliveryFixture(t *testing.T) (*NativeService, *recoveryCompletionStore, *recoveryDeliveryLauncher, model.RecoveryGate) {
	t.Helper()
	db, err := state.Open(statetest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := &recoveryCompletionStore{DB: db}
	launcher := &recoveryDeliveryLauncher{}
	svc := NewNativeWithExecution(store, testProjectResolver{root: "/repo"}, &fakePlanningLauncher{}, launcher)
	epic := createPouredWorkEpic(t, svc, "Recovery delivery")
	proposal, err := svc.SubmitProposal(t.Context(), SubmitProposalRequest{EpicID: epic.ID, Manifest: ProposalManifest{EpicID: epic.ID, MolID: pouredIssueID(t, svc, epic.ID, "mol"), Project: "/repo", Nodes: []ManifestNode{{Key: "work", Type: "implementation", Requirement: "required", AcceptanceCriteria: []string{"passes"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DecidePlanGate(t.Context(), epic.ID, "approve", PlanGateDecisionRequest{ExpectedRevision: proposal.Revision, ExpectedHash: proposal.ContentHash}); err != nil {
		t.Fatal(err)
	}
	first := launcher.calls[0]
	gate, err := svc.CreateRecoveryGate(t.Context(), first.AttemptID, first.AgentToken, "Continue?", "Blocked", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.RecordFactoryRecoveryCheckpoint(t.Context(), gate.IssueID, model.FactoryAttemptResult{Branch: first.Branch, CommitSHA: "checkpoint"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	return svc, store, launcher, gate
}

func TestPendingRecoveryConfirmsBeforeValidatingDirtyWorkspace(t *testing.T) {
	svc, store, launcher, gate := recoveryDeliveryFixture(t)
	store.fail = true
	if _, err := svc.ResolveRecoveryGate(t.Context(), gate.IssueID, "resume", "Continue"); err == nil {
		t.Fatal("expected failed finalization")
	}
	launcher.delivered = true
	launcher.handoffErr = errors.New("factory worktree has uncommitted changes")
	resolved, err := svc.ResolveRecoveryGate(t.Context(), gate.IssueID, "resume", "Continue")
	if err != nil || resolved.Resolution != "resume" || len(launcher.recoveries) != 1 {
		t.Fatalf("pending delivery not reconciled: %#v, %v, sends=%d", resolved, err, len(launcher.recoveries))
	}
}

func TestRecoveryCompletionOutlivesDeliveryDeadline(t *testing.T) {
	svc, store, launcher, gate := recoveryDeliveryFixture(t)
	launcher.expire = true
	if _, err := svc.ResolveRecoveryGate(t.Context(), gate.IssueID, "resume", "Continue"); err != nil {
		t.Fatalf("confirmed delivery failed to finalize after deadline: %v", err)
	}
	stored, found, err := store.GetFactoryRecoveryGate(t.Context(), gate.IssueID)
	if err != nil || !found || stored.Resolution != "resume" {
		t.Fatalf("gate not durably completed: %#v, %v", stored, err)
	}
}

func TestRecoveryCheckpointFailureIsActionable(t *testing.T) {
	svc, _, launcher, gate := recoveryDeliveryFixture(t)
	launcher.handoffErr = errors.New("factory worktree has uncommitted changes")
	_, err := svc.ResolveRecoveryGate(t.Context(), gate.IssueID, "resume", "Continue")
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("checkpoint error is not actionable: %v", err)
	}
}
