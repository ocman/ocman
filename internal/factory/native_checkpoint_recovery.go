package factory

import (
	"context"
	"fmt"

	"github.com/NoUseFreak/ocman/internal/factory/model"
)

// Keep the original checkpoint in the frozen policy and recovery history.
// Only completing the attempt atomically publishes the replacement checkpoint.
func checkpointRebaseApproval(attempt model.FactoryAttempt, head string) string {
	p := attempt.FrozenPolicy
	return fmt.Sprintf("Accept rebased Factory checkpoint: attempt=%q repository=%q branch=%q old=%s new=%s", attempt.ID, p.Repository, p.Branch, p.CheckpointSHA, head)
}

func (s *NativeService) validateAttemptCheckpoint(ctx context.Context, attempt model.FactoryAttempt) (string, error) {
	p := attempt.FrozenPolicy
	if p.ForceComplete {
		// The merged pull request is the authority for a force-completed
		// delivery; its pre-merge rebase cannot contain the old checkpoint.
		return s.implementation.ValidateImplementationCheckpoint(ctx, p.Repository, p.Branch, "")
	}
	head, err := s.implementation.ValidateImplementationCheckpoint(ctx, p.Repository, p.Branch, p.CheckpointSHA)
	if err == nil || err.Error() != "factory handoff does not include the accepted checkpoint" || p.CheckpointSHA == "" {
		return head, err
	}
	store := s.store
	// The empty checkpoint skips only ancestry. Clean worktree, branch and
	// pushed-upstream validation still run, on every completion validation.
	head, checkErr := s.implementation.ValidateImplementationCheckpoint(ctx, p.Repository, p.Branch, "")
	if checkErr != nil {
		return "", checkErr
	}
	approved, checkErr := store.FactoryAttemptHasRecoveryResponse(ctx, attempt.ID, checkpointRebaseApproval(attempt, head))
	if checkErr != nil {
		return "", checkErr
	}
	if !approved {
		return "", err
	}
	return head, nil
}

// RequestCheckpointRecovery asks a human to accept exactly the current clean,
// pushed HEAD for this attempt. It never changes policy or accepts the rewrite.
func (s *NativeService) RequestCheckpointRecovery(ctx context.Context, attemptID, token string) (RecoveryGate, error) {
	store := s.store
	if s.implementation == nil {
		return RecoveryGate{}, ErrFactoryUnavailable
	}
	valid, err := store.ValidateFactoryAttemptToken(ctx, attemptID, token)
	if err != nil {
		return RecoveryGate{}, err
	}
	if !valid {
		return RecoveryGate{}, fmt.Errorf("%w: factory implementation attempt is not active", ErrInvalidRequest)
	}
	attempt, found, err := store.GetFactoryAttempt(ctx, attemptID)
	if err != nil {
		return RecoveryGate{}, err
	}
	p := attempt.FrozenPolicy
	if !found || p.CheckpointSHA == "" || p.Branch == "" {
		return RecoveryGate{}, fmt.Errorf("%w: attempt has no recoverable implementation checkpoint", ErrInvalidRequest)
	}
	if step, err := s.verificationStep(ctx, attempt); err != nil || step != nil {
		if err != nil {
			return RecoveryGate{}, err
		}
		return RecoveryGate{}, fmt.Errorf("%w: verification cannot rewrite a checkpoint", ErrInvalidRequest)
	}
	if _, err := s.implementation.ValidateImplementationCheckpoint(ctx, p.Repository, p.Branch, p.CheckpointSHA); err == nil || err.Error() != "factory handoff does not include the accepted checkpoint" {
		if err != nil {
			return RecoveryGate{}, err
		}
		return RecoveryGate{}, fmt.Errorf("%w: checkpoint ancestry is intact", ErrInvalidRequest)
	}
	head, err := s.implementation.ValidateImplementationCheckpoint(ctx, p.Repository, p.Branch, "")
	if err != nil {
		return RecoveryGate{}, err
	}
	title := attempt.WorkID
	issues, err := s.store.ListFactoryIssues(ctx, attempt.EpicID)
	if err != nil {
		return RecoveryGate{}, err
	}
	for _, issue := range issues {
		if issue.ID == attempt.WorkID {
			title = fmt.Sprintf("%s (%s)", issue.Title, issue.ID)
			break
		}
	}
	return s.CreateRecoveryGate(ctx, attemptID, token,
		"Accept the rebased checkpoint for "+title+"?",
		"The original checkpoint remains recorded. Only the exact attempt, repository, branch, old SHA and pushed new SHA below are authorized. A changed HEAD needs a new decision. Completion still requires the normal checks.",
		[]string{checkpointRebaseApproval(attempt, head)})
}
