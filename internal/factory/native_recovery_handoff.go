package factory

import (
	"context"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory/model"
)

// A paused writer can yield to ready work only after it has stopped and its
// progress is a verified checkpoint. The recovery gate and unfinished Issue stay.
func (s *NativeService) handoffRecoveryWorkspace(ctx context.Context, epicID string) error {
	s.recoveryMu.Lock()
	defer s.recoveryMu.Unlock()
	store, ok := s.store.(interface {
		ListFactoryAttempts(context.Context, string) ([]model.FactoryAttempt, error)
		GetFactoryRecoveryGate(context.Context, string) (model.RecoveryGate, bool, error)
		RecordFactoryRecoveryCheckpoint(context.Context, string, model.FactoryAttemptResult, time.Time) error
	})
	if !ok {
		return nil
	}
	attempts, err := store.ListFactoryAttempts(ctx, epicID)
	if err != nil {
		return err
	}
	issues, err := s.store.ListFactoryIssues(ctx, epicID)
	if err != nil {
		return err
	}
	for _, attempt := range attempts {
		if attempt.Phase != model.FactoryAttemptActive || attempt.FrozenPolicy.Profile != "factory-implement/v1" {
			continue
		}
		for _, issue := range issues {
			if issue.Kind != "gate" || issue.Status != "open" {
				continue
			}
			gate, found, err := store.GetFactoryRecoveryGate(ctx, issue.ID)
			if err != nil {
				return err
			}
			if !found || gate.AttemptID != attempt.ID || gate.Resolution != "open" {
				continue
			}
			if attempt.Result != nil && attempt.Result.Summary == "Recovery workspace checkpoint "+gate.IssueID {
				continue
			}
			if attempt.FrozenPolicy.Branch == "" {
				continue
			} // Legacy sessions need explicit workspace adoption first.
			if attempt.Session.ID != "" {
				if err := s.implementation.StopImplementationSession(ctx, attempt.Session); err != nil {
					return err
				}
			}
			head, err := s.implementation.ValidateImplementationCheckpoint(ctx, attempt.FrozenPolicy.Repository, attempt.FrozenPolicy.Branch, attempt.FrozenPolicy.CheckpointSHA)
			if err != nil {
				return err
			}
			result := model.FactoryAttemptResult{Branch: attempt.FrozenPolicy.Branch, CommitSHA: head, TargetBranch: attempt.FrozenPolicy.TargetBranch}
			if err := store.RecordFactoryRecoveryCheckpoint(ctx, gate.IssueID, result, time.Now()); err != nil {
				return err
			}
		}
	}
	return nil
}
