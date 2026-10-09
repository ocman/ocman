package factory

import (
	"context"
	"errors"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory/model"
	"github.com/sirupsen/logrus"
)

// errHandoffBlocked is already logged (once per reason) by handoffRecoveryWorkspace.
var errHandoffBlocked = errors.New("factory recovery workspace handoff blocked")

// resumeQueuedRecoveries delivers resume decisions that were queued while
// another Issue held the Epic workspace. Still busy means keep waiting.
func (s *NativeService) resumeQueuedRecoveries(ctx context.Context) {
	queue, ok := s.store.(nativeRecoveryQueueStore)
	if !ok {
		return
	}
	gates, err := queue.ListFactoryQueuedRecoveryResumes(ctx)
	if err != nil {
		logrus.WithError(err).Warn("Factory could not list queued recovery resumes")
		return
	}
	for _, gate := range gates {
		s.recoveryMu.Lock()
		_, err := s.resolveRecoveryGate(ctx, gate.IssueID, "resume", gate.Response)
		s.recoveryMu.Unlock()
		if err != nil {
			// A failed delivery leaves the gate resume_pending with a visible
			// "Retry resume"; it leaves this queue either way.
			logrus.WithError(err).WithField("gate", gate.IssueID).Warn("Factory could not deliver a queued recovery resume")
		}
	}
}

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
			// Abort makes OpenCode emit session.idle, which wakes Dispatch; aborting
			// again on every wake turned a blocked handoff into a hot loop.
			lastReason, stopped := s.handoffBlocked[gate.IssueID]
			if attempt.Session.ID != "" && !stopped {
				if err := s.implementation.StopImplementationSession(ctx, attempt.Session); err != nil {
					return err
				}
			}
			head, err := s.implementation.ValidateImplementationCheckpoint(ctx, attempt.FrozenPolicy.Repository, attempt.FrozenPolicy.Branch, attempt.FrozenPolicy.CheckpointSHA)
			if err != nil {
				if s.handoffBlocked == nil {
					s.handoffBlocked = map[string]string{}
				}
				if !stopped || lastReason != err.Error() {
					logrus.WithError(err).WithFields(logrus.Fields{"epic": epicID, "gate": gate.IssueID}).Warn("Factory recovery workspace handoff blocked")
				}
				s.handoffBlocked[gate.IssueID] = err.Error()
				return errHandoffBlocked
			}
			delete(s.handoffBlocked, gate.IssueID)
			result := model.FactoryAttemptResult{Branch: attempt.FrozenPolicy.Branch, CommitSHA: head, TargetBranch: attempt.FrozenPolicy.TargetBranch}
			if err := store.RecordFactoryRecoveryCheckpoint(ctx, gate.IssueID, result, time.Now()); err != nil {
				return err
			}
		}
	}
	return nil
}
