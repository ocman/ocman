package factory

import (
	"context"
	"fmt"

	"github.com/NoUseFreak/ocman/internal/factory/model"
)

// StopEpic is the confirmed user's unconditional exit. It does not validate,
// reset, deliver, or remove a workspace. Failed stops leave the Epic paused.
func (s *NativeService) StopEpic(ctx context.Context, epicID string) error {
	store, ok := s.store.(interface {
		nativeClosureStore
		nativeEpicLifecycleStore
		ListFactoryAttempts(context.Context, string) ([]model.FactoryAttempt, error)
	})
	if !ok {
		return ErrFactoryUnavailable
	}
	s.authorityMu.Lock()
	defer s.authorityMu.Unlock()
	s.projectRequestMu.Lock()
	defer s.projectRequestMu.Unlock()
	s.recoveryMu.Lock()
	defer s.recoveryMu.Unlock()
	s.planningMu.Lock()
	defer s.planningMu.Unlock()
	s.implementationMu.Lock()
	defer s.implementationMu.Unlock()
	if err := store.SetFactoryEpicPaused(ctx, epicID, true); err != nil {
		return err
	}
	attempts, err := store.ListFactoryAttempts(ctx, epicID)
	if err != nil {
		return err
	}
	for _, attempt := range attempts {
		if attempt.Phase == model.FactoryAttemptTerminal {
			continue
		}
		select {
		case <-s.cancelVerificationChecks(attempt.ID):
		case <-ctx.Done():
			return ctx.Err()
		}
		if attempt.Session.ID == "" {
			continue
		}
		if attempt.FrozenPolicy.Profile == "factory-implement/v1" {
			if s.implementation == nil {
				return ErrFactoryUnavailable
			}
			err = s.implementation.StopImplementationSession(ctx, attempt.Session)
		} else {
			if s.planning == nil {
				return ErrFactoryUnavailable
			}
			err = s.planning.StopPlanningSession(ctx, attempt.Session)
		}
		if err != nil {
			return fmt.Errorf("stop %s: %w", attempt.WorkID, err)
		}
	}
	if err := store.CloseFactoryEpic(ctx, epicID, true); err != nil {
		return err
	}
	s.wakeDispatch()
	return nil
}
