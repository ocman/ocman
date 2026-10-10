package factory

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory/model"
)

type nativeVerificationFollowUpStore interface {
	AddFactoryVerificationFollowUps(context.Context, string, model.GraphMutation, time.Time, bool) ([]string, error)
	GetFactoryAttempt(context.Context, string) (model.FactoryAttempt, bool, error)
}

// AddVerificationFollowUps records a failing validator's follow-up work as
// runnable Epic tickets, ends the verification attempt, and lets dispatch run
// the follow-ups and then a fresh verification. No human approval round.
func (s *NativeService) AddVerificationFollowUps(ctx context.Context, attemptID, agentToken string, mutation GraphMutation) ([]string, error) {
	store, ok := s.store.(nativeVerificationFollowUpStore)
	tokens, tokensOK := s.store.(nativeAttemptCompletionStore)
	if !ok || !tokensOK {
		return nil, ErrFactoryUnavailable
	}
	if strings.TrimSpace(attemptID) == "" || strings.TrimSpace(agentToken) == "" {
		return nil, fmt.Errorf("%w: attempt_id and attempt_token are required", ErrInvalidRequest)
	}
	valid, err := tokens.ValidateFactoryAttemptToken(ctx, attemptID, agentToken)
	if err != nil {
		return nil, err
	}
	if !valid {
		return nil, ErrActionNotPermitted
	}
	attempt, found, err := store.GetFactoryAttempt(ctx, attemptID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("%w: factory attempt is unavailable", ErrInvalidRequest)
	}
	// Validate first, then stop the validator while its attempt still holds
	// the workspace, and only then release it: follow-ups never run beside a
	// validator that is still executing checks. A failed stop changes nothing.
	if _, err := store.AddFactoryVerificationFollowUps(ctx, attemptID, mutation, time.Now(), false); err != nil {
		return nil, err
	}
	// The call comes from this session, so aborting it may cancel ctx.
	ctx = context.WithoutCancel(ctx)
	if s.implementation != nil && attempt.Session.ID != "" {
		if err := s.implementation.StopImplementationSession(ctx, attempt.Session); err != nil {
			return nil, fmt.Errorf("stop verification session: %w", err)
		}
	}
	created, err := store.AddFactoryVerificationFollowUps(ctx, attemptID, mutation, time.Now(), true)
	if err != nil {
		return nil, err
	}
	s.forgetVerificationChecks(attemptID)
	s.wakeDispatch()
	return created, nil
}
