package factory

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory/model"
)

type Issue struct {
	Workflow       *model.WorkflowStep           `json:"workflow,omitempty"`
	ID             string                        `json:"id"`
	EpicID         string                        `json:"epicId"`
	Project        string                        `json:"project"`
	ParentID       string                        `json:"parentId,omitempty"`
	Requirement    string                        `json:"requirement,omitempty"`
	FormulaID      string                        `json:"formulaId,omitempty"`
	FormulaVersion int                           `json:"formulaVersion,omitempty"`
	FormulaHash    string                        `json:"formulaHash,omitempty"`
	Bindings       map[string]string             `json:"bindings,omitempty"`
	Kind           string                        `json:"kind"`
	Title          string                        `json:"title"`
	Status         string                        `json:"status"`
	Outcome        string                        `json:"outcome,omitempty"`
	OutcomeReason  string                        `json:"outcomeReason,omitempty"`
	Conclusion     string                        `json:"conclusion,omitempty"`
	PRURL          string                        `json:"prUrl,omitempty"`
	DispatchState  string                        `json:"dispatchState,omitempty"`
	Blockers       []model.NativeIssueBlocker    `json:"blockers,omitempty"`
	DependsOn      []model.NativeIssueDependency `json:"dependsOn,omitempty"`
	RetryAt        int64                         `json:"retryAt,omitempty"`
	RetryAttempts  int                           `json:"retryAttempts,omitempty"`
	Description    string                        `json:"description,omitempty"`
	PlanRevision   int                           `json:"planRevision,omitempty"`
	ManifestKey    string                        `json:"manifestKey,omitempty"`
	CreatedAt      int64                         `json:"createdAt,omitempty"`
	RemovedAt      int64                         `json:"removedAt,omitempty"`
	AttemptID      string                        `json:"attemptId,omitempty"`
	Session        PlanningSession               `json:"session,omitempty"`
	Recovery       *RecoveryGate                 `json:"recovery,omitempty"`
	Authority      *AuthorityEscalationGate      `json:"authority,omitempty"`
	ProjectRequest *ProjectRequestGate           `json:"projectRequest,omitempty"`
}

type RecoveryGate = model.RecoveryGate
type AuthorityEscalationGate = model.AuthorityEscalationGate
type ProjectRequestGate = model.ProjectRequestGate
type IssueComment = model.NativeIssueComment

func (s *NativeService) ListIssues(ctx context.Context, epicID string) ([]Issue, error) {
	issues, err := s.store.ListFactoryIssues(ctx, epicID)
	if errors.Is(err, model.ErrNativeEpicNotFound) {
		err = ErrWorkEpicNotFound
	}
	result := nativeIssues(issues)
	if store, ok := s.store.(nativePlanningStore); ok && err == nil {
		attempts, attemptErr := store.ListFactoryAttempts(ctx, epicID)
		if attemptErr != nil {
			return nil, attemptErr
		}
		latest := map[string]model.FactoryAttempt{}
		for _, attempt := range attempts {
			if current, exists := latest[attempt.WorkID]; !exists || current.Sequence < attempt.Sequence {
				latest[attempt.WorkID] = attempt
			}
		}
		for i := range result {
			if attempt, exists := latest[result[i].ID]; exists {
				result[i].AttemptID, result[i].Session = attempt.ID, attempt.Session
				if attempt.Result != nil {
					result[i].Conclusion = attempt.Result.Summary
					result[i].PRURL = attempt.Result.PRURL
				}
			}
			if recovery, ok := s.store.(nativeRecoveryStore); ok {
				gate, found, recoveryErr := recovery.GetFactoryRecoveryGate(ctx, result[i].ID)
				if recoveryErr != nil {
					return nil, recoveryErr
				}
				if found {
					result[i].Recovery = &gate
				}
			}
			if authority, ok := s.store.(nativeAuthorityStore); ok {
				gate, found, authorityErr := authority.GetFactoryAuthorityEscalationGate(ctx, result[i].ID)
				if authorityErr != nil {
					return nil, authorityErr
				}
				if found {
					result[i].Authority = &gate
				}
			}
		}
	}
	return result, err
}

func (s *NativeService) ListIssueComments(ctx context.Context, epicID, issueID string) ([]IssueComment, error) {
	store, ok := s.store.(nativeCommentStore)
	if !ok {
		return nil, ErrFactoryUnavailable
	}
	comments, err := store.ListFactoryIssueComments(ctx, epicID, issueID)
	if errors.Is(err, model.ErrInvalidGraphMutation) {
		err = fmt.Errorf("%w: issue not found", ErrInvalidRequest)
	}
	return comments, err
}

func (s *NativeService) AddIssueComment(ctx context.Context, epicID, issueID, actor, body string) (IssueComment, error) {
	store, ok := s.store.(nativeCommentStore)
	if !ok {
		return IssueComment{}, ErrFactoryUnavailable
	}
	comment, err := store.AppendFactoryIssueComment(ctx, epicID, issueID, actor, body, time.Now())
	if errors.Is(err, model.ErrInvalidGraphMutation) {
		err = fmt.Errorf("%w: issue and comment body are required", ErrInvalidRequest)
	}
	return comment, err
}

func (s *NativeService) ListRemovedIssues(ctx context.Context, epicID string) ([]Issue, error) {
	store, ok := s.store.(interface {
		ListRemovedFactoryIssues(context.Context, string) ([]model.NativeIssue, error)
	})
	if !ok {
		return nil, ErrFactoryUnavailable
	}
	issues, err := store.ListRemovedFactoryIssues(ctx, epicID)
	if errors.Is(err, model.ErrNativeEpicNotFound) {
		err = ErrWorkEpicNotFound
	}
	return nativeIssues(issues), err
}

func nativeEpic(epic model.NativeEpic) WorkEpic {
	origin := FormulaOriginCustom
	if epic.FormulaID == BuiltInTracerFormula().ID {
		origin = FormulaOriginBuiltIn
	}
	return WorkEpic{ID: epic.ID, Status: epic.Status, Goal: epic.Goal, Brief: epic.Brief, InitialProject: epic.InitialProject, Projects: epic.Projects, InstantiationID: epic.InstantiationID, FormulaID: epic.FormulaID, FormulaVersion: epic.FormulaVersion, FormulaRevision: epic.FormulaVersion, FormulaHash: epic.FormulaHash, FormulaOrigin: origin}
}
func nativeEpics(epics []model.NativeEpic) []WorkEpic {
	out := make([]WorkEpic, len(epics))
	for i := range epics {
		out[i] = nativeEpic(epics[i])
	}
	return out
}
func nativeIssues(issues []model.NativeIssue) []Issue {
	out := make([]Issue, len(issues))
	for i := range issues {
		issue := issues[i]
		out[i] = Issue{ID: issue.ID, EpicID: issue.EpicID, Project: issue.Project, ParentID: issue.ParentID, Requirement: issue.Requirement, FormulaID: issue.FormulaID, FormulaVersion: issue.FormulaVersion, FormulaHash: issue.FormulaHash, Bindings: issue.Bindings, Kind: issue.Kind, Title: issue.Title, Status: issue.Status, Description: issue.Description, PlanRevision: issue.PlanRevision, ManifestKey: issue.ManifestKey, Outcome: issue.Outcome, OutcomeReason: issue.OutcomeReason, DispatchState: issue.DispatchState, Blockers: issue.Blockers, DependsOn: issue.DependsOn, RetryAt: issue.RetryAt, RetryAttempts: issue.RetryAttempts, CreatedAt: issue.CreatedAt, RemovedAt: issue.RemovedAt, ProjectRequest: issue.ProjectRequestGate}
		out[i].Workflow = issue.Workflow
	}
	return out
}
