package autoapprove

import (
	"context"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/state"
)

func (s *Service) persistLifecycle(asked askedPermission, sessionID, permissionID string, update state.PermissionLifecycle) {
	if s == nil || s.deps.Store == nil || !asked.lifecycleEnabled {
		return
	}
	update.Platform = asked.platformID
	update.SessionID = sessionID
	update.PermissionID = permissionID
	update.Directory = asked.directory
	update.RequestedAt = asked.askedAt
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), approvalPersistenceTimeout)
		defer cancel()
		if err := s.deps.Store.UpsertPermissionLifecycle(ctx, update); err != nil {
			log.WithError(err).WithFields(log.Fields{"sessionID": sessionID, "permissionID": permissionID}).Warn("autoapprove: failed to persist permission lifecycle")
		}
	}()
}

func (s *Service) setLifecycleMethod(asked askedPermission, sessionID, permissionID string, method state.PermissionEvaluationMethod, result state.PermissionEvaluationResult) {
	key := autoApproveKey(sessionID, permissionID)
	s.autoApproveMu.Lock()
	status := s.autoApprove[key]
	preempted := false
	if status != nil {
		status.evaluationMethod = method
		preempted = method == state.PermissionEvaluationJudge && status.manualResolvedAt > 0 &&
			(status.judgeCompletedAt == 0 || status.manualResolvedAt <= status.judgeCompletedAt)
	}
	s.autoApproveMu.Unlock()
	s.persistLifecycle(asked, sessionID, permissionID, state.PermissionLifecycle{
		EvaluationMethod:  method,
		EvaluationResult:  result,
		ManuallyPreempted: preempted,
	})
}

func (s *Service) completeJudgeLifecycle(asked askedPermission, sessionID, permissionID string, result JudgeResult) {
	completedAt := time.Now().UnixMilli()
	evaluation := state.PermissionEvaluationUnsafe
	if result.EvaluationFailed {
		evaluation = state.PermissionEvaluationError
	} else if result.Verdict == verdictSafe {
		evaluation = state.PermissionEvaluationSafe
	}
	key := autoApproveKey(sessionID, permissionID)
	s.autoApproveMu.Lock()
	if status := s.autoApprove[key]; status != nil {
		status.judgeCompletedAt = completedAt
	}
	s.autoApproveMu.Unlock()
	s.persistLifecycle(asked, sessionID, permissionID, state.PermissionLifecycle{
		JudgeCompletedAt: completedAt,
		EvaluationResult: evaluation,
	})
}

func (s *Service) recordUserLifecycle(sessionID, permissionID, reply string, resolvedAt int64) {
	asked, ok := s.getAsked(sessionID, permissionID)
	if !ok {
		return
	}
	var resolution state.PermissionResolution
	switch reply {
	case "once":
		resolution = state.PermissionResolutionUserOnce
	case "always":
		resolution = state.PermissionResolutionUserAlways
	case "reject":
		resolution = state.PermissionResolutionUserRejected
	case "cancel", "cancelled":
		resolution = state.PermissionResolutionCancelled
	default:
		return
	}
	key := autoApproveKey(sessionID, permissionID)
	s.autoApproveMu.Lock()
	status := s.autoApprove[key]
	preempted := false
	if status != nil {
		status.manualResolvedAt = resolvedAt
		status.manualResolution = resolution
		preempted = status.evaluationMethod == state.PermissionEvaluationJudge &&
			(status.judgeCompletedAt == 0 || resolvedAt <= status.judgeCompletedAt)
	}
	s.autoApproveMu.Unlock()
	if !asked.lifecycleEnabled {
		return
	}
	s.persistLifecycle(asked, sessionID, permissionID, state.PermissionLifecycle{
		ResolvedAt: resolvedAt, Resolution: resolution, ManuallyPreempted: preempted,
	})
}

func (s *Service) persistRecordedManualLifecycle(asked askedPermission, sessionID, permissionID string) {
	key := autoApproveKey(sessionID, permissionID)
	s.autoApproveMu.Lock()
	status := s.autoApprove[key]
	var resolution state.PermissionResolution
	var resolvedAt int64
	if status != nil {
		resolution = status.manualResolution
		resolvedAt = status.manualResolvedAt
	}
	s.autoApproveMu.Unlock()
	if resolution == "" {
		return
	}
	s.persistLifecycle(asked, sessionID, permissionID, state.PermissionLifecycle{
		ResolvedAt: resolvedAt,
		Resolution: resolution,
	})
}
