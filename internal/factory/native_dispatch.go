package factory

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory/model"
)

// Dispatch admits ready executable Issues in deterministic order until
// configured capacity is full. Plan and Materialization never enter this path.
func (s *NativeService) Dispatch(ctx context.Context) error {
	store, ok := s.store.(nativePlanningStore)
	if !ok || s.implementation == nil {
		return nil
	}
	if err := s.reconcileImplementationSessions(ctx, store); err != nil {
		return err
	}
	if err := s.observeMergeGates(ctx); err != nil {
		return err
	}
	if delays, ok := s.store.(nativeDelayStore); ok {
		if err := delays.WakeFactoryRetries(ctx, time.Now()); err != nil {
			return err
		}
	}
	type candidate struct {
		issue model.NativeIssue
		epic  model.NativeEpic
	}
	var ready []candidate
	epics, err := s.store.ListFactoryEpics(ctx)
	if err != nil {
		return err
	}
	for _, epic := range epics {
		if epic.Status != "open" {
			continue
		}
		if completionStore, ok := s.store.(nativeAttemptCompletionStore); ok {
			if err := completionStore.EnsureFactoryDeliveryIssue(ctx, epic.ID); err != nil {
				return err
			}
		}
		issues, err := s.store.ListFactoryIssues(ctx, epic.ID)
		if err != nil {
			return err
		}
		for _, issue := range issues {
			if (issue.Kind == "implementation" || issue.Kind == "task" || issue.Kind == "delivery") && issue.DispatchState == "ready" {
				ready = append(ready, candidate{issue, epic})
			}
		}
	}
	sort.Slice(ready, func(i, j int) bool {
		if ready[i].issue.CreatedAt != ready[j].issue.CreatedAt {
			return ready[i].issue.CreatedAt < ready[j].issue.CreatedAt
		}
		return ready[i].issue.ID < ready[j].issue.ID
	})
	for _, next := range ready {
		stage := "implementation"
		if next.issue.Kind == "delivery" {
			stage = "delivery"
		}
		prompt, err := s.issuePrompt(ctx, next.epic, next.issue.ID, stage)
		if err != nil {
			return err
		}
		epic, attempt, err := store.ClaimFactoryImplementation(ctx, next.epic.ID, next.issue.ID, "factory-implement/v1", time.Now())
		if err != nil {
			continue
		} // A saturated project must not stall other projects.
		branch := "factory/" + epic.ID
		baseRef := ""
		repository := attempt.FrozenPolicy.Repository
		if completionStore, ok := s.store.(nativeAttemptCompletionStore); ok {
			var branchErr error
			if attempt.FrozenPolicy.Branch != "" {
				branch = attempt.FrozenPolicy.Branch
			} else {
				// Existing PR-based epics resolve their branch once when adopting checkpoints.
				var previousPR string
				previousPR, branchErr = completionStore.FactoryEpicPRURL(ctx, epic.ID, repository)
				if branchErr == nil && previousPR != "" {
					branch, baseRef, branchErr = s.implementation.ResolveImplementationBranch(ctx, repository, branch, previousPR, attempt.FrozenPolicy)
				}
			}
			if branchErr == nil {
				checkpoint := attempt.FrozenPolicy.CheckpointSHA
				// A retry of the same Issue may retain its own committed progress.
				if attempt.Sequence > 1 && checkpoint != "" {
					_, branchErr = s.implementation.ValidateImplementationCheckpoint(ctx, repository, branch, checkpoint)
					checkpoint = ""
				}
				var workspaceBase string
				if branchErr == nil {
					attempt.FrozenPolicy.TargetBranch, workspaceBase, branchErr = s.implementation.PrepareImplementationWorkspace(ctx, repository, branch, checkpoint, attempt.FrozenPolicy.TargetBranch)
				}
				if workspaceBase != "" && attempt.FrozenPolicy.BaseRef != "" {
					workspaceBase = attempt.FrozenPolicy.BaseRef
				}
				if baseRef == "" {
					baseRef = workspaceBase
				}
			}
			if branchErr == nil {
				attempt.FrozenPolicy.Branch = branch
				attempt.FrozenPolicy.BaseRef = baseRef
				branchErr = completionStore.SetFactoryAttemptWorkspace(ctx, attempt.ID, attempt.FrozenPolicy)
			}
			if branchErr != nil {
				_, _ = store.FailFactoryAttempt(context.WithoutCancel(ctx), attempt.ID, model.FactoryAttemptFailure{Type: "launch_failed", Message: "Implementation branch could not be resolved: " + branchErr.Error()}, time.Now())
				continue
			}
		}
		description := next.issue.Description
		if strings.HasPrefix(next.issue.OutcomeReason, "Project request rejected: ") {
			description = strings.TrimSpace(description + "\n\n" + next.issue.OutcomeReason)
		}
		request := ImplementationSessionRequest{Model: attempt.FrozenPolicy.Model, EpicID: epic.ID, WorkID: next.issue.ID, AttemptID: attempt.ID, AgentToken: attempt.AgentToken, Repository: repository, Projects: attempt.FrozenPolicy.Projects, Title: next.issue.Title, Description: description, Branch: branch, BaseRef: baseRef, Profile: "factory-implement/v1", TargetBranch: attempt.FrozenPolicy.TargetBranch, Delivery: attempt.FrozenPolicy.Delivery, PermissionRules: attempt.FrozenPolicy.PermissionRules}
		request.Prompt = prompt
		request.Verification = next.issue.Workflow != nil && next.issue.Workflow.Kind == "verification"
		session, launchErr := s.implementation.LaunchImplementationSession(ctx, request)
		if launchErr != nil {
			if session.ID != "" {
				_ = s.implementation.StopImplementationSession(context.WithoutCancel(ctx), session)
			}
			_, _ = store.FailFactoryAttempt(context.WithoutCancel(ctx), attempt.ID, model.FactoryAttemptFailure{Type: "launch_failed", Message: "Implementation Session could not be launched: " + launchErr.Error()}, time.Now())
			continue
		}
		if session.ID == "" || session.Platform == "" {
			_, _ = store.FailFactoryAttempt(context.WithoutCancel(ctx), attempt.ID, model.FactoryAttemptFailure{Type: "launch_failed", Message: "Implementation Session returned no session"}, time.Now())
			continue
		}
		if activated, err := store.ActivateFactoryAttempt(ctx, attempt.ID, session, time.Now()); err != nil || !activated {
			_ = s.implementation.StopImplementationSession(context.WithoutCancel(ctx), session)
			_, _ = store.FailFactoryAttempt(context.WithoutCancel(ctx), attempt.ID, model.FactoryAttemptFailure{Type: "activation_failed", Message: "Implementation Session could not be recorded"}, time.Now())
			continue
		}
		if err := s.implementation.PromptImplementationSession(ctx, session, request); err != nil {
			_ = s.implementation.StopImplementationSession(context.WithoutCancel(ctx), session)
			_, _ = store.FailFactoryAttempt(context.WithoutCancel(ctx), attempt.ID, model.FactoryAttemptFailure{Type: "prompt_failed", Message: "Implementation Session could not be prompted"}, time.Now())
		}
	}
	return nil
}

func (s *NativeService) observeMergeGates(ctx context.Context) error {
	s.mergeGateMu.Lock()
	defer s.mergeGateMu.Unlock()
	store, ok := s.store.(nativeMergeGateStore)
	if !ok {
		return nil
	}
	observations, err := store.ListFactoryMergeGateDeliveries(ctx, time.Now().Add(-factoryMergeGatePollInterval), 8)
	if err != nil {
		return err
	}
	errs := make(chan error, len(observations))
	var wg sync.WaitGroup
	for _, observation := range observations {
		wg.Add(1)
		go func(observation model.FactoryDeliveryObservation) {
			defer wg.Done()
			observation.ObservedAt = time.Now().UnixMilli()
			pollCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			status, head, observeErr := s.implementation.ObserveImplementationDelivery(pollCtx, observation.PRURL, observation.Policy)
			if observeErr != nil {
				observation.Status, observation.Reason = "unavailable", "Forge could not verify the Project Delivery PR: "+observeErr.Error()
			} else if head != observation.CommitSHA {
				observation.Status, observation.Reason = "changed", "Project Delivery PR no longer points to its recorded commit. Restore or replace the PR."
			} else {
				observation.Status, observation.Reason = status, ""
			}
			if err := store.RecordFactoryDeliveryObservation(ctx, observation); err != nil {
				errs <- err
			}
		}(observation)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		return err
	}
	return nil
}

func (s *NativeService) reconcileImplementationSessions(ctx context.Context, store nativePlanningStore) error {
	s.implementationMu.Lock()
	defer s.implementationMu.Unlock()
	attempts, err := store.ListFactoryAttempts(ctx, "")
	if err != nil {
		return err
	}
	for _, attempt := range attempts {
		if attempt.Phase == model.FactoryAttemptStopping && attempt.FrozenPolicy.Profile == "factory-implement/v1" {
			if err := s.implementation.StopImplementationSession(context.WithoutCancel(ctx), attempt.Session); err != nil {
				continue
			}
			if _, err := store.FailFactoryAttempt(ctx, attempt.ID, model.FactoryAttemptFailure{Type: "handoff_finalize_failed", Message: "Implementation handoff did not finish recording"}, time.Now()); err != nil {
				return fmt.Errorf("recover stopping Implementation attempt: %w", err)
			}
			continue
		}
		if attempt.Phase != model.FactoryAttemptActive || attempt.FrozenPolicy.Profile != "factory-implement/v1" || attempt.Session.ID == "" {
			continue
		}
		if recovery, ok := s.store.(nativeRecoveryStore); ok {
			paused, err := recovery.IsFactoryAttemptRecoveryPaused(ctx, attempt.ID)
			if err != nil {
				return err
			}
			if paused {
				continue
			}
		}
		alive, probeErr := s.implementation.ProbeImplementationSession(ctx, attempt.Session)
		if probeErr != nil || alive {
			continue
		}
		if _, err := store.FailFactoryAttempt(ctx, attempt.ID, model.FactoryAttemptFailure{Type: "interrupted_runtime", Message: "Implementation Session is no longer available"}, time.Now()); err != nil {
			return fmt.Errorf("recover Implementation attempt: %w", err)
		}
	}
	return nil
}

func (s *NativeService) Queue(ctx context.Context) ([]DispatchItem, error) {
	store, ok := s.store.(nativePlanningStore)
	if !ok {
		return nil, ErrFactoryUnavailable
	}
	epics, err := s.store.ListFactoryEpics(ctx)
	if err != nil {
		return nil, err
	}
	attempts, err := store.ListFactoryAttempts(ctx, "")
	if err != nil {
		return nil, err
	}
	byWork := map[string]model.FactoryAttempt{}
	for _, attempt := range attempts {
		if current, ok := byWork[attempt.WorkID]; !ok || current.Sequence < attempt.Sequence {
			byWork[attempt.WorkID] = attempt
		}
	}
	var queue []DispatchItem
	for _, epic := range epics {
		issues, err := s.store.ListFactoryIssues(ctx, epic.ID)
		if err != nil {
			return nil, err
		}
		for _, issue := range issues {
			if issue.Kind != "implementation" && issue.Kind != "task" && issue.Kind != "delivery" {
				continue
			}
			item := DispatchItem{ID: issue.ID, EpicID: epic.ID, Title: issue.Title, Project: issue.Project, OutcomeReason: issue.OutcomeReason, Blockers: issue.Blockers, RetryAt: issue.RetryAt, RetryAttempts: issue.RetryAttempts}
			attempt, attempted := byWork[issue.ID]
			if attempted {
				item.AttemptID, item.Session, item.Outcome = attempt.ID, attempt.Session, string(attempt.Outcome)
			}
			if attempted && attempt.Phase != model.FactoryAttemptTerminal {
				item.State = DispatchRunning
			} else if epic.Status == "closed" {
				continue
			} else if epic.Status == "paused" && issue.DispatchState == string(DispatchReady) {
				item.State = DispatchPaused
			} else if attempted {
				if issue.DispatchState == string(DispatchRetryWait) || issue.DispatchState == string(DispatchReady) {
					item.State = DispatchState(issue.DispatchState)
				} else {
					item.State = DispatchCompleted
				}
			} else if issue.DispatchState != "" {
				item.State = DispatchState(issue.DispatchState)
			} else {
				continue
			}
			queue = append(queue, item)
		}
	}
	sort.Slice(queue, func(i, j int) bool {
		if queue[i].State != queue[j].State {
			return queue[i].State == DispatchRunning
		}
		return queue[i].ID < queue[j].ID
	})
	return queue, nil
}
