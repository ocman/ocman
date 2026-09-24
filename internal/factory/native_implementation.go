package factory

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory/model"
)

// IsImplementationSession cheaply identifies sessions whose out-of-profile
// permissions need Factory authority handling.
func (s *NativeService) IsImplementationSession(ctx context.Context, session string) (bool, error) {
	store, ok := s.store.(nativeAuthorityStore)
	if !ok {
		return false, nil
	}
	return store.IsFactoryImplementationSession(ctx, session)
}

// EscalatePermission diverts only requests excluded by the frozen profile.
// Other prompts continue through OpenCode's direct permission flow.
func (s *NativeService) EscalatePermission(ctx context.Context, session, requestID, permission, target string) (AuthorityEscalationGate, bool, error) {
	store, ok := s.store.(nativeAuthorityStore)
	if !ok {
		return AuthorityEscalationGate{}, false, ErrFactoryUnavailable
	}
	if session == "" || requestID == "" || permission == "" {
		return AuthorityEscalationGate{}, false, errors.New("session, request, and permission are required")
	}
	return store.CreateFactoryAuthorityEscalationGate(ctx, session, requestID, permission, target, time.Now())
}

func (s *NativeService) CompleteAttempt(ctx context.Context, attemptID, agentToken, summary, prURL string) error {
	s.implementationMu.Lock()
	defer s.implementationMu.Unlock()
	store, ok := s.store.(nativeAttemptCompletionStore)
	if !ok {
		return ErrFactoryUnavailable
	}
	summary, prURL = strings.TrimSpace(summary), strings.TrimSpace(prURL)
	if attemptID == "" || agentToken == "" || summary == "" {
		return fmt.Errorf("%w: attempt ID, token, and summary are required", ErrInvalidRequest)
	}
	valid, err := store.ValidateFactoryAttemptToken(ctx, attemptID, agentToken)
	if err != nil {
		return err
	}
	if !valid {
		return fmt.Errorf("%w: factory implementation attempt is not active", ErrInvalidRequest)
	}
	attemptStore, ok := s.store.(interface {
		GetFactoryAttempt(context.Context, string) (model.FactoryAttempt, bool, error)
	})
	if !ok || s.implementation == nil {
		return ErrFactoryUnavailable
	}
	attempt, found, err := attemptStore.GetFactoryAttempt(ctx, attemptID)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("%w: factory implementation attempt is not active", ErrInvalidRequest)
	}
	if attempt.Phase == model.FactoryAttemptTerminal && attempt.Outcome == model.FactoryAttemptSucceeded {
		if attempt.Result != nil && attempt.Result.Summary == summary && attempt.Result.PRURL == prURL {
			return nil
		}
		return fmt.Errorf("%w: factory implementation attempt already completed with a different result", ErrInvalidRequest)
	}
	var parsedPR *url.URL
	if attempt.FrozenPolicy.Delivery {
		var parseErr error
		parsedPR, parseErr = url.ParseRequestURI(prURL)
		if parseErr != nil || (parsedPR.Scheme != "http" && parsedPR.Scheme != "https") || parsedPR.Host == "" {
			return fmt.Errorf("%w: delivery requires a pull request URL", ErrInvalidRequest)
		}
	} else if prURL != "" {
		return fmt.Errorf("%w: implementation handoffs require a commit checkpoint, not a pull request", ErrInvalidRequest)
	}
	if attempt.FrozenPolicy.Branch == "" {
		// Older sessions did not record their workspace. Adopt it without a PR
		// lookup or branch rotation; the session may already be on a successor.
		branch, target, err := s.implementation.ResolveImplementationWorkspace(ctx, attempt.FrozenPolicy.Repository, "factory/"+attempt.EpicID, attempt.Session)
		if err != nil {
			return factoryHandoffError(err)
		}
		attempt.FrozenPolicy.Branch, attempt.FrozenPolicy.TargetBranch = branch, target
	}
	if attempt.FrozenPolicy.Delivery {
		response := "Force-complete the delivery attempt using merged PR #" + path.Base(parsedPR.Path)
		attempt.FrozenPolicy.ForceComplete, err = store.FactoryAttemptHasRecoveryResponse(ctx, attemptID, response)
		if err != nil {
			return err
		}
	}
	result := model.FactoryAttemptResult{SchemaVersion: 2, Summary: summary, PRURL: prURL}
	validate := func(ctx context.Context) error {
		head, err := s.implementation.ValidateImplementationCheckpoint(ctx, attempt.FrozenPolicy.Repository, attempt.FrozenPolicy.Branch, attempt.FrozenPolicy.CheckpointSHA)
		if err != nil {
			return err
		}
		if result.CommitSHA != "" && result.CommitSHA != head {
			return errors.New("factory branch changed while completing the handoff")
		}
		result.Branch, result.CommitSHA, result.TargetBranch = attempt.FrozenPolicy.Branch, head, attempt.FrozenPolicy.TargetBranch
		if attempt.FrozenPolicy.Delivery {
			return s.implementation.ValidateImplementationHandoff(ctx, attempt.FrozenPolicy.Repository, attempt.FrozenPolicy.Branch, "", prURL, attempt.FrozenPolicy)
		}
		return nil
	}
	if err := validate(ctx); err != nil {
		return factoryHandoffError(err)
	}
	stopping, err := store.StopFactoryAttempt(context.WithoutCancel(ctx), attempt.ID, time.Now())
	if err != nil {
		return err
	}
	if !stopping {
		return fmt.Errorf("%w: factory implementation attempt is not active", ErrInvalidRequest)
	}
	// The completed session is archived by CompleteFactoryImplementationAttempt,
	// not deleted: its transcript stays browsable. Re-validate so a push that
	// raced the stop transition is still caught.
	if err := validate(context.WithoutCancel(ctx)); err != nil {
		return factoryHandoffError(err)
	}
	changed, err := store.CompleteFactoryImplementationAttempt(context.WithoutCancel(ctx), attemptID, agentToken, result, time.Now())
	if err != nil {
		return err
	}
	if !changed {
		completed, found, getErr := attemptStore.GetFactoryAttempt(context.WithoutCancel(ctx), attemptID)
		if getErr != nil {
			return getErr
		}
		if found && completed.Phase == model.FactoryAttemptTerminal && completed.Outcome == model.FactoryAttemptSucceeded && completed.Result != nil && *completed.Result == result {
			return nil
		}
		return fmt.Errorf("%w: factory implementation attempt is not active", ErrInvalidRequest)
	}
	if attempt.FrozenPolicy.Delivery {
		s.notifyEpicDelivered(context.WithoutCancel(ctx), attempt.EpicID, summary, prURL, attempt.Session.Platform, attempt.Session.ID)
	}
	select {
	case s.dispatchWake <- struct{}{}:
	default:
	}
	return nil
}

func factoryHandoffError(err error) error {
	switch err.Error() {
	case "factory worktree has uncommitted changes",
		"factory handoff does not include the accepted checkpoint",
		"factory branch changed while completing the handoff",
		"delivery pull request must be open and target the recorded branch",
		"factory branch has not been pushed with an upstream",
		"factory branch HEAD has not been pushed",
		"factory shared branch worktree was not found",
		"factory epic already uses an open pull request",
		"completed pull request requires a replacement",
		"pull request URL has no numeric identifier",
		"pull request URL does not match the delivery target",
		"pull request must be ready for review",
		"pull request does not publish the shared Factory branch HEAD":
		return fmt.Errorf("%w: %w", ErrInvalidRequest, err)
	default:
		return err
	}
}
