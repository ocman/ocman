package factory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory/model"
)

func (s *NativeService) DecidePlanGate(ctx context.Context, epicID, action string, req PlanGateDecisionRequest) (PlanGate, error) {
	store, ok := s.store.(nativePlanningStore)
	if !ok {
		return PlanGate{}, ErrFactoryUnavailable
	}
	if req.ExpectedRevision < 1 || strings.TrimSpace(req.ExpectedHash) == "" {
		return PlanGate{}, fmt.Errorf("%w: plan revision and hash are required", ErrInvalidRequest)
	}
	implementationModel := strings.TrimSpace(req.ImplementationModel)
	if implementationModel != "" {
		provider, name, valid := strings.Cut(implementationModel, "/")
		if !valid || provider == "" || name == "" || len(implementationModel) > 300 || strings.ContainsAny(implementationModel, " \t\r\n") {
			return PlanGate{}, fmt.Errorf("%w: implementation model must be provider/model", ErrInvalidRequest)
		}
	}
	gate, err := store.DecideFactoryPlanGate(ctx, epicID, action, req.ExpectedRevision, req.ExpectedHash, strings.TrimSpace(req.Feedback), implementationModel)
	if errors.Is(err, sql.ErrNoRows) {
		return PlanGate{}, fmt.Errorf("%w: factory Plan gate is unavailable", ErrInvalidRequest)
	}
	if err == nil && action == "approve" {
		issues, listErr := s.store.ListFactoryIssues(ctx, epicID)
		if listErr != nil {
			return nativePlanGate(gate), listErr
		}
		for _, issue := range issues {
			if issue.Kind == "materialization" && issue.Status == "open" {
				if _, err := s.Materialize(ctx, epicID, issue.ID); err != nil {
					return nativePlanGate(gate), err
				}
			}
		}
		_ = s.Dispatch(ctx)
	}
	return nativePlanGate(gate), err
}

// Materialize creates implementation work and dispatches ready issues.
func (s *NativeService) Materialize(ctx context.Context, epicID, issueID string) (Materialization, error) {
	s.materializationMu.Lock()
	defer s.materializationMu.Unlock()
	store, ok := s.store.(nativePlanningStore)
	if !ok {
		return Materialization{}, ErrFactoryUnavailable
	}
	materialization, err := store.MaterializeFactoryPlan(ctx, epicID, issueID, "factory-materialize/v1", time.Now())
	if errors.Is(err, model.ErrNativeEpicNotFound) {
		err = ErrWorkEpicNotFound
	}
	if err == nil {
		_ = s.Dispatch(ctx)
	}
	return Materialization{ID: materialization.ID, IssueID: materialization.IssueID, ProposalRevision: materialization.ProposalRevision, ProposalHash: materialization.ProposalHash, ManifestKey: materialization.ManifestKey, ImplementationID: materialization.ImplementationID, Issues: materialization.Issues}, err
}

func nativePlanGate(gate model.NativePlanGate) PlanGate {
	return PlanGate{ImplementationModel: gate.ImplementationModel, IssueID: gate.IssueID, ProposalRevision: gate.ProposalRevision, ProposalHash: gate.ProposalHash, Outcome: gate.Outcome, Resolution: gate.Resolution, Feedback: gate.Feedback, ReviewIssueIDs: gate.ReviewIssueIDs}
}

// ClaimPlan records a prepared Attempt before exposing the bounded Planning Session.
func (s *NativeService) ClaimPlan(ctx context.Context, epicID, issueID string) (ClaimedPlan, error) {
	s.planningMu.Lock()
	defer s.planningMu.Unlock()
	store, ok := s.store.(nativePlanningStore)
	if !ok || s.planning == nil {
		return ClaimedPlan{}, ErrFactoryUnavailable
	}
	epic, attempt, err := store.ClaimFactoryPlan(ctx, epicID, issueID, planningProfile, time.Now())
	if errors.Is(err, model.ErrNativeEpicNotFound) {
		return ClaimedPlan{}, ErrWorkEpicNotFound
	}
	if err != nil {
		return ClaimedPlan{}, err
	}
	projects := make([]string, len(epic.Projects))
	for i, project := range epic.Projects {
		projects[i] = project.Path
	}
	request := PlanningSessionRequest{EpicID: epic.ID, WorkID: issueID, AttemptID: attempt.ID, AgentToken: attempt.AgentToken, Repository: epic.InitialProject, Projects: projects, Title: "PLAN " + issueID + " (@factory)", PermissionRules: attempt.FrozenPolicy.PermissionRules}
	request.Model = attempt.FrozenPolicy.Model
	if projectRequests, ok := s.store.(nativeProjectRequestStore); ok {
		_, request.ScopeExpansion, err = projectRequests.GetFactoryProjectRequestGateForPlan(ctx, issueID)
		if err != nil {
			_, _ = store.FailFactoryAttempt(context.WithoutCancel(ctx), attempt.ID, model.FactoryAttemptFailure{Type: "scope_lookup_failed", Message: "Scope expansion could not be loaded"}, time.Now())
			return ClaimedPlan{}, err
		}
	}
	stage := "planning"
	if request.ScopeExpansion {
		stage = "scope_expansion"
	}
	request.Prompt, err = s.issuePrompt(ctx, epic, issueID, stage)
	if err != nil {
		_, _ = store.FailFactoryAttempt(context.WithoutCancel(ctx), attempt.ID, model.FactoryAttemptFailure{Type: "formula_prompt_failed", Message: "Formula prompt could not be loaded"}, time.Now())
		return ClaimedPlan{}, err
	}
	session, launchErr := s.planning.LaunchPlanningSession(ctx, request)
	if launchErr != nil {
		if session.ID != "" {
			_ = s.planning.StopPlanningSession(context.WithoutCancel(ctx), session)
		}
		failureType := "launch_failed"
		if request.ScopeExpansion {
			failureType = "scope_launch_failed"
		}
		_, _ = store.FailFactoryAttempt(context.WithoutCancel(ctx), attempt.ID, model.FactoryAttemptFailure{Type: failureType, Message: "Planning Session could not be launched"}, time.Now())
		return ClaimedPlan{}, ErrFactoryUnavailable
	}
	if activated, err := store.ActivateFactoryAttempt(ctx, attempt.ID, session, time.Now()); err != nil || !activated {
		_ = s.planning.StopPlanningSession(context.WithoutCancel(ctx), session)
		failureType := "activation_failed"
		if request.ScopeExpansion {
			failureType = "scope_activation_failed"
		}
		_, _ = store.FailFactoryAttempt(context.WithoutCancel(ctx), attempt.ID, model.FactoryAttemptFailure{Type: failureType, Message: "Planning Session could not be recorded"}, time.Now())
		if err == nil {
			err = errors.New("factory attempt was no longer prepared")
		}
		return ClaimedPlan{}, fmt.Errorf("recording Planning Session: %w", err)
	}
	if err := s.planning.PromptPlanningSession(ctx, session, request); err != nil {
		_ = s.planning.StopPlanningSession(context.WithoutCancel(ctx), session)
		_, _ = store.FailFactoryAttempt(context.WithoutCancel(ctx), attempt.ID, model.FactoryAttemptFailure{Type: "prompt_failed", Message: "Planning Session could not be prompted"}, time.Now())
		return ClaimedPlan{}, ErrFactoryUnavailable
	}
	attempt.Phase, attempt.Session = model.FactoryAttemptActive, session
	return ClaimedPlan{Attempt: attempt, Session: session}, nil
}
