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
	var cancel []string
	for _, attempt := range stop {
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
	for _, id := range cancel {
		s.forgetVerificationChecks(id)
	}
	s.wakeDispatch()
	return deferred, nil
}
