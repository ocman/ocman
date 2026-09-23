package factory

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory/model"
	"github.com/sirupsen/logrus"
)

func (s *NativeService) Start(ctx context.Context) error {
	s.planningMu.Lock()
	defer s.planningMu.Unlock()
	store, ok := s.store.(nativePlanningStore)
	if !ok || s.planning == nil {
		return nil
	}
	ctx = context.WithoutCancel(ctx)
	attempts, err := store.ListFactoryAttempts(ctx, "")
	if err != nil {
		return err
	}
	for _, attempt := range attempts {
		if attempt.Phase == model.FactoryAttemptTerminal {
			continue
		}
		if attempt.FrozenPolicy.Profile == "factory-implement/v1" {
			if recovery, ok := s.store.(nativeRecoveryStore); ok {
				paused, err := recovery.IsFactoryAttemptRecoveryPaused(ctx, attempt.ID)
				if err != nil {
					return err
				}
				if paused {
					continue
				}
			}
			if s.implementation == nil {
				continue
			}
			if attempt.Phase == model.FactoryAttemptActive && attempt.FrozenPolicy.DeliveryRemoteRepo == "" {
				if err := s.implementation.StopImplementationSession(context.WithoutCancel(ctx), attempt.Session); err != nil {
					continue
				}
				if _, err := store.FailFactoryAttempt(ctx, attempt.ID, model.FactoryAttemptFailure{Type: "delivery_migration", Message: "Implementation attempt predates shared delivery"}, time.Now()); err != nil {
					return fmt.Errorf("migrate Implementation attempt: %w", err)
				}
				continue
			}
			if attempt.Phase == model.FactoryAttemptStopping {
				if err := s.implementation.StopImplementationSession(context.WithoutCancel(ctx), attempt.Session); err != nil {
					continue
				}
			}
			if attempt.Phase == model.FactoryAttemptActive && attempt.Session.ID != "" {
				alive, err := s.implementation.ProbeImplementationSession(ctx, attempt.Session)
				if err != nil || alive {
					continue
				}
			}
			_, err := store.FailFactoryAttempt(ctx, attempt.ID, model.FactoryAttemptFailure{Type: "interrupted_startup", Message: "Implementation Session was not durably available after restart"}, time.Now())
			if err != nil {
				return fmt.Errorf("recover Implementation attempt: %w", err)
			}
			continue
		}
		if attempt.FrozenPolicy.Profile != planningProfile {
			continue
		}
		issues, err := s.store.ListFactoryIssues(ctx, attempt.EpicID)
		if err != nil {
			return err
		}
		isPlan := false
		for _, issue := range issues {
			if issue.ID == attempt.WorkID && issue.Kind == "plan" {
				isPlan = true
				break
			}
		}
		if !isPlan {
			continue
		}
		if attempt.Phase == model.FactoryAttemptActive && attempt.Session.ID != "" && attempt.Session.Platform != "" {
			alive, err := s.planning.ProbePlanningSession(ctx, attempt.Session)
			if err != nil || alive {
				continue
			}
		}
		failureType := "interrupted_startup"
		if projectRequests, ok := s.store.(nativeProjectRequestStore); ok {
			if _, scopeExpansion, gateErr := projectRequests.GetFactoryProjectRequestGateForPlan(ctx, attempt.WorkID); gateErr != nil {
				return gateErr
			} else if scopeExpansion {
				failureType = "scope_interrupted_startup"
			}
		}
		if _, err := store.FailFactoryAttempt(ctx, attempt.ID, model.FactoryAttemptFailure{Type: failureType, Message: "Planning Session was not durably available after restart"}, time.Now()); err != nil {
			return fmt.Errorf("recover Planning attempt: %w", err)
		}
	}
	if s.implementation != nil {
		s.startOnce.Do(func() {
			s.dispatchWG.Add(1)
			go func() {
				defer s.dispatchWG.Done()
				s.runDispatch()
			}()
		})
	}
	return nil
}
func (s *NativeService) runDispatch() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-s.stop
		cancel()
	}()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := s.Dispatch(ctx); err != nil && ctx.Err() == nil {
				logrus.WithError(err).Error("Factory dispatch failed")
			}
		case <-s.dispatchWake:
			if err := s.Dispatch(ctx); err != nil && ctx.Err() == nil {
				logrus.WithError(err).Error("Factory dispatch failed")
			}
		case <-s.stop:
			return
		}
	}
}
func (s *NativeService) Close() {
	s.closeOnce.Do(func() {
		close(s.stop)
		s.dispatchWG.Wait()
	})
}
func (*NativeService) Status(context.Context) Status {
	return Status{Health: HealthHealthy, Idle: true, DispatchOwner: true, Dispatch: []DispatchItem{}}
}

func (s *NativeService) GetCapacityPolicy(ctx context.Context) (CapacityPolicy, error) {
	store, ok := s.store.(capacityPolicyStore)
	if !ok {
		return CapacityPolicy{}, ErrFactoryUnavailable
	}
	return store.GetFactoryCapacityPolicy(ctx)
}

func (s *NativeService) SetCapacityPolicy(ctx context.Context, policy CapacityPolicy) (CapacityPolicy, error) {
	if err := validateCapacityPolicy(policy); err != nil {
		return CapacityPolicy{}, err
	}
	if s.projects == nil && len(policy.ProjectOverrides) != 0 {
		return CapacityPolicy{}, ErrProjectNotLocalGit
	}
	canonical := make(map[string]int, len(policy.ProjectOverrides))
	for project, capacity := range policy.ProjectOverrides {
		root, err := s.canonicalProject(ctx, project)
		if err != nil {
			return CapacityPolicy{}, err
		}
		if existing, exists := canonical[root]; exists && existing != capacity {
			return CapacityPolicy{}, fmt.Errorf("%w: factory project override aliases disagree", ErrInvalidRequest)
		}
		canonical[root] = capacity
	}
	policy.ProjectOverrides = canonical
	store, ok := s.store.(capacityPolicyStore)
	if !ok {
		return CapacityPolicy{}, ErrFactoryUnavailable
	}
	if err := store.SetFactoryCapacityPolicy(ctx, policy); err != nil {
		return CapacityPolicy{}, err
	}
	return policy, nil
}

func validateCapacityPolicy(policy CapacityPolicy) error {
	for _, capacity := range append([]int{policy.GlobalCapacity, policy.ProjectCapacity}, mapValues(policy.ProjectOverrides)...) {
		if capacity < 1 || capacity > maxCapacity {
			return fmt.Errorf("%w: factory capacity must be between 1 and %d", ErrInvalidRequest, maxCapacity)
		}
	}
	return nil
}

func mapValues(values map[string]int) []int {
	result := make([]int, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	return result
}

func (s *NativeService) canonicalProject(ctx context.Context, path string) (string, error) {
	if !filepath.IsAbs(path) || s.projects == nil {
		return "", ErrProjectNotLocalGit
	}
	project, err := s.projects.ResolveLocalProject(ctx, path)
	if err != nil || !filepath.IsAbs(project) {
		return "", fmt.Errorf("%w: %w", ErrProjectNotLocalGit, err)
	}
	project = filepath.Clean(project)
	if strings.ContainsAny(project, "*?[") {
		return "", fmt.Errorf("%w: project paths cannot contain wildcards", ErrInvalidRequest)
	}
	return project, nil
}
