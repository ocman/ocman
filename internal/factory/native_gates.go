package factory

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/ocman/ocman/internal/factory/model"
)

func (s *NativeService) CreateRecoveryGate(ctx context.Context, attemptID, agentToken, question, reason string, choices []string) (RecoveryGate, error) {
	if strings.TrimSpace(attemptID) == "" || strings.TrimSpace(agentToken) == "" || strings.TrimSpace(question) == "" || strings.TrimSpace(reason) == "" {
		return RecoveryGate{}, fmt.Errorf("%w: attempt, token, question, and reason are required", ErrInvalidRequest)
	}
	valid, err := s.store.ValidateFactoryAttemptToken(ctx, attemptID, agentToken)
	if err != nil {
		return RecoveryGate{}, err
	}
	if !valid {
		return RecoveryGate{}, fmt.Errorf("%w: factory implementation attempt token is invalid", ErrInvalidRequest)
	}
	gate, err := s.store.CreateFactoryRecoveryGate(ctx, attemptID, strings.TrimSpace(question), strings.TrimSpace(reason), choices, time.Now())
	if err == nil {
		s.wakeDispatch()
	}
	return gate, err
}

func (s *NativeService) RequestProject(ctx context.Context, attemptID, agentToken, project, reason string) (ProjectRequestGate, error) {
	project, reason = strings.TrimSpace(project), strings.TrimSpace(reason)
	if attemptID == "" || agentToken == "" || project == "" || reason == "" || !filepath.IsAbs(project) {
		return ProjectRequestGate{}, fmt.Errorf("%w: attempt, token, absolute project path, and reason are required", ErrInvalidRequest)
	}
	valid, err := s.store.ValidateFactoryAttemptToken(ctx, attemptID, agentToken)
	if err != nil {
		return ProjectRequestGate{}, err
	}
	if !valid {
		return ProjectRequestGate{}, ErrActionNotPermitted
	}
	gate, err := s.store.CreateFactoryProjectRequestGate(ctx, attemptID, project, reason, time.Now())
	if err == nil {
		s.wakeDispatch()
	}
	return gate, err
}

func (s *NativeService) ResolveProjectRequest(ctx context.Context, gateID, action, response string, acknowledge bool) (ProjectRequestGate, error) {
	s.projectRequestMu.Lock()
	defer s.projectRequestMu.Unlock()
	gate, found, err := s.store.GetFactoryProjectRequestGate(ctx, gateID)
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
		attempt, found, err := s.store.GetFactoryAttempt(ctx, gate.AttemptID)
		if err != nil || !found {
			return ProjectRequestGate{}, fmt.Errorf("%w: factory project request attempt is unavailable", ErrInvalidRequest)
		}
		return s.deliverProjectRejection(ctx, gate, attempt)
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
		if err = s.store.UpsertFactoryLocalExecutionAck(ctx, "local", canonical, "factory-implement", "v1", "operator", time.Now()); err != nil {
			return ProjectRequestGate{}, err
		}
	} else if action != "reject" {
		return ProjectRequestGate{}, fmt.Errorf("%w: invalid project request action", ErrInvalidRequest)
	}
	if action == "approve" {
		if s.implementation == nil {
			return ProjectRequestGate{}, errors.New("implementation launcher is unavailable")
		}
		attempt, found, getErr := s.store.GetFactoryAttempt(ctx, gate.AttemptID)
		if getErr != nil || !found {
			return ProjectRequestGate{}, fmt.Errorf("%w: factory project request attempt is unavailable", ErrInvalidRequest)
		}
		if attempt.Session.ID != "" {
			if err := s.implementation.StopImplementationSession(context.WithoutCancel(ctx), attempt.Session); err != nil {
				return ProjectRequestGate{}, fmt.Errorf("stop implementation for scope expansion: %w", err)
			}
		}
	}
	gate, attempt, err := s.store.ResolveFactoryProjectRequestGate(ctx, gateID, action, canonical, response, time.Now())
	if err != nil {
		return ProjectRequestGate{}, err
	}
	if action == "reject" {
		return s.deliverProjectRejection(ctx, gate, attempt)
	}
	_, err = s.ClaimPlan(ctx, gate.EpicID, gate.PlanIssueID)
	return gate, err
}

func (s *NativeService) deliverProjectRejection(ctx context.Context, gate ProjectRequestGate, attempt model.FactoryAttempt) (ProjectRequestGate, error) {
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
		return s.store.CompleteFactoryProjectRequestRejection(context.WithoutCancel(ctx), gate.IssueID, time.Now())
	}
	completed, err := s.store.FailFactoryProjectRequestRejection(context.WithoutCancel(ctx), gate.IssueID, time.Now())
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
	gate, err := s.resolveRecoveryGate(ctx, gateID, action, response)
	s.recoveryMu.Unlock()
	if err == nil && action == "retry" {
		_ = s.Dispatch(ctx)
	}
	return gate, err
}

func (s *NativeService) resolveRecoveryGate(ctx context.Context, gateID, action, response string) (RecoveryGate, error) {
	if action != "resume" && action != "retry" && action != "cancel" {
		return RecoveryGate{}, fmt.Errorf("%w: invalid recovery gate action", ErrInvalidRequest)
	}
	response = strings.TrimSpace(response)
	gate, found, err := s.store.GetFactoryRecoveryGate(ctx, gateID)
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
	queued := gate
	gate, attempt, err := s.store.ResolveFactoryRecoveryGate(ctx, gateID, action, response, time.Now())
	if errors.Is(err, model.ErrRecoveryWorkspaceBusy) && !pending {
		// Another Issue holds the Epic workspace. Keep the decision; Dispatch
		// delivers it once the workspace is released instead of asking the user
		// to come back and click again.
		if response == "" {
			response = "Continue" // ponytail: a non-empty response marks the gate as queued; the delivered prompt is equivalent
		}
		if queued.Response != response { // Dispatch re-checks every pass; record the decision once
			if err := s.store.QueueFactoryRecoveryResume(ctx, gateID, response, time.Now()); err != nil {
				return RecoveryGate{}, err
			}
			queued.Response = response
		}
		return queued, nil
	}
	if err != nil {
		return RecoveryGate{}, err
	}
	if action == "resume" {
		if s.implementation == nil {
			return RecoveryGate{}, errors.New("implementation launcher is unavailable")
		}
		delivered := false
		if pending {
			delivered, err = s.implementation.RecoveryResponseDelivered(ctx, attempt.Session, gate.IssueID)
			if err != nil {
				return RecoveryGate{}, err
			}
		}
		if !delivered {
			if attempt.Result != nil && strings.HasPrefix(attempt.Result.Summary, "Recovery workspace checkpoint ") {
				if _, err := s.implementation.ValidateImplementationCheckpoint(ctx, attempt.FrozenPolicy.Repository, attempt.FrozenPolicy.Branch, attempt.FrozenPolicy.CheckpointSHA); err != nil {
					return RecoveryGate{}, fmt.Errorf("validate resumed Factory workspace: %w", factoryHandoffError(err))
				}
			}
			deliveryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancel()
			if err := s.implementation.ResumeImplementationSession(deliveryCtx, attempt.Session, gate.IssueID, gate.Response); err != nil {
				return RecoveryGate{}, fmt.Errorf("deliver Factory recovery response: %w", err)
			}
		}
		completionCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		return s.store.CompleteFactoryRecoveryGate(completionCtx, gateID, time.Now())
	}
	if (action == "retry" || action == "cancel") && s.implementation != nil && attempt.Session.ID != "" {
		_ = s.implementation.StopImplementationSession(context.WithoutCancel(ctx), attempt.Session)
	}
	s.wakeDispatch()
	return gate, nil
}

func (s *NativeService) ResolveAuthorityEscalationGate(ctx context.Context, gateID, action string) (AuthorityEscalationGate, error) {
	s.authorityMu.Lock()
	defer s.authorityMu.Unlock()
	if action != "approve" && action != "reject" {
		return AuthorityEscalationGate{}, fmt.Errorf("%w: invalid authority escalation action", ErrInvalidRequest)
	}
	if s.implementation == nil {
		return AuthorityEscalationGate{}, errors.New("implementation launcher is unavailable")
	}
	gate, found, err := s.store.GetFactoryAuthorityEscalationGate(ctx, gateID)
	pending := action + "_pending"
	if err != nil {
		return AuthorityEscalationGate{}, err
	}
	if !found || (gate.Resolution != "open" && gate.Resolution != pending) {
		return AuthorityEscalationGate{}, fmt.Errorf("%w: authority escalation gate is unavailable", ErrInvalidRequest)
	}
	attempt, found, err := s.store.GetFactoryAttempt(ctx, gate.AttemptID)
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
		gate, _, err = s.store.ResolveFactoryAuthorityEscalationGate(ctx, gateID, action, time.Now())
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
	return s.store.CompleteFactoryAuthorityEscalationGate(ctx, gateID, action, time.Now())
}
