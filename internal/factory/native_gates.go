package factory

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory/model"
)

func (s *NativeService) DeferIssue(ctx context.Context, epicID, issueID, reason string) error {
	store, ok := s.store.(nativeDelayStore)
	if !ok {
		return ErrFactoryUnavailable
	}
	return store.DeferFactoryIssue(ctx, epicID, issueID, strings.TrimSpace(reason))
}

func (s *NativeService) ResumeIssue(ctx context.Context, epicID, issueID string) error {
	store, ok := s.store.(nativeDelayStore)
	if !ok {
		return ErrFactoryUnavailable
	}
	return store.ResumeFactoryIssue(ctx, epicID, issueID)
}

func (s *NativeService) RetryIssueAt(ctx context.Context, epicID, issueID string, wakeAt time.Time) error {
	store, ok := s.store.(nativeDelayStore)
	if !ok {
		return ErrFactoryUnavailable
	}
	if !wakeAt.After(time.Now()) {
		return errors.New("retry time must be in the future")
	}
	return store.RetryFactoryIssueAt(ctx, epicID, issueID, wakeAt)
}

func (s *NativeService) CreateRecoveryGate(ctx context.Context, attemptID, agentToken, question, reason string, choices []string) (RecoveryGate, error) {
	store, ok := s.store.(nativeRecoveryStore)
	if !ok {
		return RecoveryGate{}, ErrFactoryUnavailable
	}
	if strings.TrimSpace(attemptID) == "" || strings.TrimSpace(agentToken) == "" || strings.TrimSpace(question) == "" || strings.TrimSpace(reason) == "" {
		return RecoveryGate{}, fmt.Errorf("%w: attempt, token, question, and reason are required", ErrInvalidRequest)
	}
	tokens, ok := s.store.(nativeAttemptCompletionStore)
	if !ok {
		return RecoveryGate{}, ErrFactoryUnavailable
	}
	valid, err := tokens.ValidateFactoryAttemptToken(ctx, attemptID, agentToken)
	if err != nil {
		return RecoveryGate{}, err
	}
	if !valid {
		return RecoveryGate{}, fmt.Errorf("%w: factory implementation attempt token is invalid", ErrInvalidRequest)
	}
	return store.CreateFactoryRecoveryGate(ctx, attemptID, strings.TrimSpace(question), strings.TrimSpace(reason), choices, time.Now())
}

func (s *NativeService) RequestProject(ctx context.Context, attemptID, agentToken, project, reason string) (ProjectRequestGate, error) {
	store, ok := s.store.(nativeProjectRequestStore)
	if !ok {
		return ProjectRequestGate{}, ErrFactoryUnavailable
	}
	project, reason = strings.TrimSpace(project), strings.TrimSpace(reason)
	if attemptID == "" || agentToken == "" || project == "" || reason == "" || !filepath.IsAbs(project) {
		return ProjectRequestGate{}, fmt.Errorf("%w: attempt, token, absolute project path, and reason are required", ErrInvalidRequest)
	}
	tokens, ok := s.store.(nativeAttemptCompletionStore)
	if !ok {
		return ProjectRequestGate{}, ErrFactoryUnavailable
	}
	valid, err := tokens.ValidateFactoryAttemptToken(ctx, attemptID, agentToken)
	if err != nil {
		return ProjectRequestGate{}, err
	}
	if !valid {
		return ProjectRequestGate{}, ErrActionNotPermitted
	}
	return store.CreateFactoryProjectRequestGate(ctx, attemptID, project, reason, time.Now())
}

func (s *NativeService) ResolveProjectRequest(ctx context.Context, gateID, action, response string, acknowledge bool) (ProjectRequestGate, error) {
	s.projectRequestMu.Lock()
	defer s.projectRequestMu.Unlock()
	store, ok := s.store.(nativeProjectRequestStore)
	if !ok {
		return ProjectRequestGate{}, ErrFactoryUnavailable
	}
	gate, found, err := store.GetFactoryProjectRequestGate(ctx, gateID)
	if err != nil {
		return ProjectRequestGate{}, err
	}
	response = strings.TrimSpace(response)
	if action == "reject" && response == "" {
		return ProjectRequestGate{}, fmt.Errorf("%w: project rejection feedback is required", ErrInvalidRequest)
	}
	if action == "reject" && gate.Resolution == "reject_pending" {
		if response != gate.Response {
			return ProjectRequestGate{}, fmt.Errorf("%w: project rejection response does not match the pending decision", ErrInvalidRequest)
		}
		attempts, ok := s.store.(nativeAuthorityStore)
		if !ok {
			return ProjectRequestGate{}, ErrFactoryUnavailable
		}
		attempt, found, err := attempts.GetFactoryAttempt(ctx, gate.AttemptID)
		if err != nil || !found {
			return ProjectRequestGate{}, fmt.Errorf("%w: factory project request attempt is unavailable", ErrInvalidRequest)
		}
		return s.deliverProjectRejection(ctx, store, gate, attempt)
	}
	if found && action == "approve" && gate.Resolution == "approved" {
		if !acknowledge {
			return ProjectRequestGate{}, ErrAcknowledgementRequired
		}
		issues, listErr := s.store.ListFactoryIssues(ctx, gate.EpicID)
		if listErr != nil {
			return ProjectRequestGate{}, listErr
		}
		for _, issue := range issues {
			if issue.ID == gate.PlanIssueID && issue.Status == "open" {
				_, err = s.ClaimPlan(ctx, gate.EpicID, gate.PlanIssueID)
				return gate, err
			}
		}
		return gate, nil
	}
	if !found || gate.Resolution != "open" {
		return ProjectRequestGate{}, fmt.Errorf("%w: factory project request is unavailable", ErrInvalidRequest)
	}
	canonical := ""
	if action == "approve" {
		if !acknowledge {
			return ProjectRequestGate{}, ErrAcknowledgementRequired
		}
		canonical, err = s.canonicalProject(ctx, gate.RequestedProject)
		if err != nil {
			return ProjectRequestGate{}, err
		}
		epic, getErr := s.store.GetFactoryEpic(ctx, gate.EpicID)
		if getErr != nil {
			return ProjectRequestGate{}, getErr
		}
		for _, project := range epic.Projects {
			if project.Path == canonical {
				return ProjectRequestGate{}, fmt.Errorf("%w: project is already admitted", ErrInvalidRequest)
			}
		}
		acks, ok := s.store.(localExecutionAckStore)
		if !ok {
			return ProjectRequestGate{}, ErrFactoryUnavailable
		}
		if err = acks.UpsertFactoryLocalExecutionAck(ctx, "local", canonical, "factory-implement", "v1", "operator", time.Now()); err != nil {
			return ProjectRequestGate{}, err
		}
	} else if action != "reject" {
		return ProjectRequestGate{}, fmt.Errorf("%w: invalid project request action", ErrInvalidRequest)
	}
	if action == "approve" {
		if s.implementation == nil {
			return ProjectRequestGate{}, errors.New("implementation launcher is unavailable")
		}
		attempts, ok := s.store.(nativeAuthorityStore)
		if !ok {
			return ProjectRequestGate{}, ErrFactoryUnavailable
		}
		attempt, found, getErr := attempts.GetFactoryAttempt(ctx, gate.AttemptID)
		if getErr != nil || !found {
			return ProjectRequestGate{}, fmt.Errorf("%w: factory project request attempt is unavailable", ErrInvalidRequest)
		}
		if attempt.Session.ID != "" {
			if err := s.implementation.StopImplementationSession(context.WithoutCancel(ctx), attempt.Session); err != nil {
				return ProjectRequestGate{}, fmt.Errorf("stop implementation for scope expansion: %w", err)
			}
		}
	}
	gate, attempt, err := store.ResolveFactoryProjectRequestGate(ctx, gateID, action, canonical, response, time.Now())
	if err != nil {
		return ProjectRequestGate{}, err
	}
	if action == "reject" {
		return s.deliverProjectRejection(ctx, store, gate, attempt)
	}
	_, err = s.ClaimPlan(ctx, gate.EpicID, gate.PlanIssueID)
	return gate, err
}

func (s *NativeService) deliverProjectRejection(ctx context.Context, store nativeProjectRequestStore, gate ProjectRequestGate, attempt model.FactoryAttempt) (ProjectRequestGate, error) {
	if s.implementation == nil {
		return ProjectRequestGate{}, errors.New("implementation launcher is unavailable")
	}
	alive, err := s.implementation.ProbeImplementationSession(ctx, attempt.Session)
	if err != nil {
		return ProjectRequestGate{}, fmt.Errorf("probe implementation for project rejection: %w", err)
	}
	if alive {
		if err := s.implementation.ResumeImplementationSession(context.WithoutCancel(ctx), attempt.Session, gate.IssueID, gate.Response); err != nil {
			return ProjectRequestGate{}, fmt.Errorf("deliver project rejection: %w", err)
		}
		return store.CompleteFactoryProjectRequestRejection(context.WithoutCancel(ctx), gate.IssueID, time.Now())
	}
	completed, err := store.FailFactoryProjectRequestRejection(context.WithoutCancel(ctx), gate.IssueID, time.Now())
	if err != nil {
		return ProjectRequestGate{}, err
	}
	select {
	case s.dispatchWake <- struct{}{}:
	default:
	}
	return completed, nil
}

func (s *NativeService) ResolveRecoveryGate(ctx context.Context, gateID, action, response string) (RecoveryGate, error) {
	s.recoveryMu.Lock()
	defer s.recoveryMu.Unlock()
	store, ok := s.store.(nativeRecoveryStore)
	if !ok {
		return RecoveryGate{}, ErrFactoryUnavailable
	}
	if action != "resume" && action != "retry" && action != "cancel" {
		return RecoveryGate{}, fmt.Errorf("%w: invalid recovery gate action", ErrInvalidRequest)
	}
	response = strings.TrimSpace(response)
	gate, found, err := store.GetFactoryRecoveryGate(ctx, gateID)
	if err != nil {
		return RecoveryGate{}, err
	}
	if !found {
		return RecoveryGate{}, fmt.Errorf("%w: factory recovery gate is unavailable", ErrInvalidRequest)
	}
	pending := action == "resume" && gate.Resolution == "resume_pending"
	if gate.Resolution != "open" && !pending {
		return RecoveryGate{}, fmt.Errorf("%w: factory recovery gate is unavailable", ErrInvalidRequest)
	}
	if pending && response != gate.Response {
		return RecoveryGate{}, fmt.Errorf("%w: factory recovery response does not match the pending decision", ErrInvalidRequest)
	}
	gate, attempt, err := store.ResolveFactoryRecoveryGate(ctx, gateID, action, response, time.Now())
	if err != nil {
		return RecoveryGate{}, err
	}
	if action == "resume" {
		if s.implementation == nil {
			return RecoveryGate{}, errors.New("implementation launcher is unavailable")
		}
		deliveryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if err := s.implementation.ResumeImplementationSession(deliveryCtx, attempt.Session, gate.IssueID, gate.Response); err != nil {
			return RecoveryGate{}, fmt.Errorf("deliver Factory recovery response: %w", err)
		}
		return store.CompleteFactoryRecoveryGate(deliveryCtx, gateID, time.Now())
	}
	if (action == "retry" || action == "cancel") && s.implementation != nil && attempt.Session.ID != "" {
		_ = s.implementation.StopImplementationSession(context.WithoutCancel(ctx), attempt.Session)
	}
	if action == "retry" {
		_ = s.Dispatch(ctx)
	}
	return gate, nil
}

func (s *NativeService) ResolveAuthorityEscalationGate(ctx context.Context, gateID, action string) (AuthorityEscalationGate, error) {
	s.authorityMu.Lock()
	defer s.authorityMu.Unlock()
	store, ok := s.store.(nativeAuthorityStore)
	if !ok {
		return AuthorityEscalationGate{}, ErrFactoryUnavailable
	}
	if action != "approve" && action != "reject" {
		return AuthorityEscalationGate{}, fmt.Errorf("%w: invalid authority escalation action", ErrInvalidRequest)
	}
	if s.implementation == nil {
		return AuthorityEscalationGate{}, errors.New("implementation launcher is unavailable")
	}
	gate, found, err := store.GetFactoryAuthorityEscalationGate(ctx, gateID)
	pending := action + "_pending"
	if err != nil {
		return AuthorityEscalationGate{}, err
	}
	if !found || (gate.Resolution != "open" && gate.Resolution != pending) {
		return AuthorityEscalationGate{}, fmt.Errorf("%w: authority escalation gate is unavailable", ErrInvalidRequest)
	}
	attempt, found, err := store.GetFactoryAttempt(ctx, gate.AttemptID)
	if err != nil {
		return AuthorityEscalationGate{}, err
	}
	if !found || attempt.Phase != model.FactoryAttemptActive {
		return AuthorityEscalationGate{}, fmt.Errorf("%w: authority escalation attempt is unavailable", ErrInvalidRequest)
	}
	reply := "reject"
	if action == "approve" {
		reply = "once"
	}
	if gate.Resolution == "open" {
		gate, _, err = store.ResolveFactoryAuthorityEscalationGate(ctx, gateID, action, time.Now())
		if err != nil {
			return AuthorityEscalationGate{}, err
		}
	}
	// Persist the decision before delivery; same-action retries only redeliver it.
	promptPending, err := s.implementation.ImplementationPermissionPending(ctx, attempt.Session, gate.RequestID)
	if err != nil {
		return AuthorityEscalationGate{}, err
	}
	if promptPending {
		if err := s.implementation.RespondImplementationPermission(context.WithoutCancel(ctx), attempt.Session, gate.RequestID, reply); err != nil {
			return AuthorityEscalationGate{}, err
		}
	}
	return store.CompleteFactoryAuthorityEscalationGate(ctx, gateID, action, time.Now())
}
