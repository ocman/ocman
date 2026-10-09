package factory

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory/model"
)

type nativeVerificationFollowUpStore interface {
	AddFactoryVerificationFollowUps(context.Context, string, model.GraphMutation, time.Time) ([]string, error)
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
	created, err := store.AddFactoryVerificationFollowUps(ctx, attemptID, mutation, time.Now())
	if err != nil {
		return nil, err
	}
	s.forgetVerificationChecks(attemptID)
	if s.implementation != nil && attempt.Session.ID != "" {
		// The call arrives from this session; stop it once the reply is out.
		go func() { _ = s.implementation.StopImplementationSession(context.WithoutCancel(ctx), attempt.Session) }()
	}
	s.wakeDispatch()
	return created, nil
}
