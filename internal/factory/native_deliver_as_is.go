package factory

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory/model"
)

type nativeDeliverAsIsStore interface {
	ListFactoryAttempts(context.Context, string) ([]model.FactoryAttempt, error)
	IsFactoryAttemptRecoveryPaused(context.Context, string) (bool, error)
	DeliverFactoryEpicAsIs(context.Context, string, []string, time.Time) ([]string, error)
}

// DeliverAsIs is the user's way out of a review loop: running validators and
// work paused for a human decision are stopped, unfinished implementation
// work is left out, and verification of the current graph is waived, so
// Factory delivers what is done. Other running work must finish first.
func (s *NativeService) DeliverAsIs(ctx context.Context, epicID string) ([]string, error) {
	store, ok := s.store.(nativeDeliverAsIsStore)
	if !ok {
		return nil, ErrFactoryUnavailable
	}
	s.recoveryMu.Lock()
	defer s.recoveryMu.Unlock()
	attempts, err := store.ListFactoryAttempts(ctx, epicID)
	if err != nil {
		return nil, err
	}
	issues, err := s.store.ListFactoryIssues(ctx, epicID)
	if err != nil {
		return nil, err
	}
	verification := map[string]bool{}
	for _, issue := range issues {
		verification[issue.ID] = issue.Workflow != nil && issue.Workflow.Kind == "verification"
	}
	var stop []model.FactoryAttempt
	for _, attempt := range attempts {
		if attempt.Phase == model.FactoryAttemptTerminal {
			continue
		}
		paused, err := store.IsFactoryAttemptRecoveryPaused(ctx, attempt.ID)
		if err != nil {
			return nil, err
		}
		if !verification[attempt.WorkID] && !paused {
			return nil, fmt.Errorf("%w: wait for running work to finish: %s", ErrInvalidRequest, attempt.WorkID)
		}
		stop = append(stop, attempt)
	}
	// The left-out work is not delivered, so its workspace must be exactly the
	// accepted checkpoint: refuse before stopping anything otherwise.
	for _, attempt := range stop {
		policy := attempt.FrozenPolicy
		if verification[attempt.WorkID] || policy.Branch == "" || s.implementation == nil {
			continue
		}
		head, err := s.implementation.ValidateImplementationCheckpoint(ctx, policy.Repository, policy.Branch, policy.CheckpointSHA)
		if err != nil {
			return nil, fmt.Errorf("%w: the workspace of paused Issue %s is not a clean, pushed checkpoint (%v); resolve its recovery first", ErrInvalidRequest, attempt.WorkID, err)
		}
		if policy.CheckpointSHA != "" && head != policy.CheckpointSHA {
			return nil, fmt.Errorf("%w: paused Issue %s has commits beyond the accepted checkpoint; resume or retry it first", ErrInvalidRequest, attempt.WorkID)
		}
	}
	var cancel []string
	for _, attempt := range stop {
		// Settle the validator's check run first, so its result message cannot
		// restart the session after it is stopped.
		select {
		case <-s.cancelVerificationChecks(attempt.ID):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		// Stop first: nothing may share the workspace with a live session.
		if s.implementation != nil && attempt.Session.ID != "" {
			if err := s.implementation.StopImplementationSession(context.WithoutCancel(ctx), attempt.Session); err != nil {
				return nil, fmt.Errorf("stop %s: %w", attempt.WorkID, err)
			}
		}
		cancel = append(cancel, attempt.ID)
	}
	deferred, err := store.DeliverFactoryEpicAsIs(ctx, epicID, cancel, time.Now())
	if errors.Is(err, model.ErrInvalidGraphMutation) {
		return nil, fmt.Errorf("%w: %w", ErrInvalidRequest, err)
	}
	if err != nil {
		return nil, err
	}
	s.wakeDispatch()
	if deferred == nil {
		deferred = []string{}
	}
	return deferred, nil
}
