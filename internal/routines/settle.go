package routines

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/state"
)

func (s *Service) settleRunning(ctx context.Context, recoverOrphans bool) error {
	runs, err := s.store.ListRunningRoutineRuns(ctx)
	if err != nil {
		return err
	}
	var result error
	for _, run := range runs {
		if run.Platform == "" || run.SessionID == "" {
			if recoverOrphans {
				result = errors.Join(result, s.finish(ctx, run, RunInterrupted, "dispatch interrupted before session linkage", ""))
			}
			continue
		}
		platform, ok := s.platforms.Get(platforms.ID(run.Platform))
		if !ok {
			continue
		}
		detail, err := platform.Session(ctx, run.SessionID, 1, 0)
		if err != nil {
			if recoverOrphans || errors.Is(err, platforms.ErrNotFound) {
				result = errors.Join(result, s.finish(ctx, run, RunInterrupted, "session is no longer available", ""))
			}
			continue
		}
		if detail == nil || detail.Session == nil {
			result = errors.Join(result, s.finish(ctx, run, RunInterrupted, "session is no longer available", ""))
			continue
		}
		_, reply := db.LatestAssistantText(detail.Messages, detail.Parts)
		switch detail.Session.Status {
		case db.StatusDone, db.StatusWaiting:
			if run.CleanupWorktree && s.now().UnixMilli()-detail.Session.TimeUpdated < settleQuietPeriod.Milliseconds() {
				continue
			}
			if run.ArchiveSessionAfterSuccess {
				// OpenCode writes once more after the turn goes idle; any newer
				// write resurfaces the session, so wait until it's quiet, then
				// ponytail: writes >5s late still resurface. Remotes use the owner clock.
				stamp := detail.Session.TimeUpdated
				if remoteID, _ := platforms.SplitPlatformID(run.Platform); remoteID == "" {
					if stamp = s.now().UnixMilli(); stamp-detail.Session.TimeUpdated < settleQuietPeriod.Milliseconds() {
						continue
					}
				}
				if err := s.store.ArchiveSession(ctx, run.Platform, run.SessionID, stamp); err != nil {
					result = errors.Join(result, err)
					continue
				}
			}
			result = errors.Join(result, s.finish(ctx, run, RunSuccess, s.cleanupRunWorktree(ctx, run, detail), reply))
		case db.StatusError:
			result = errors.Join(result, s.finish(ctx, run, RunFailure, detail.Session.Status.String(), reply))
		case db.StatusInterrupted:
			result = errors.Join(result, s.finish(ctx, run, RunInterrupted, detail.Session.Status.String(), ""))
		}
	}
	return result
}

func (s *Service) finish(ctx context.Context, run state.RoutineRun, runState, errorText, reply string) error {
	routine, err := s.store.GetRoutine(ctx, run.RoutineID)
	if err != nil {
		return err
	}
	// Only a scheduled occurrence consumes a timeout/once schedule. Manual and
	// webhook runs leave the routine's enabled state and next due time alone,
	// unless an overdue manual run claimed the due occurrence (RunNow reuses
	// it): the scheduler could never claim it again, so it must advance here.
	scheduled := run.Trigger == "schedule" || (routine.NextDueAt > 0 && run.OccurrenceAt == routine.NextDueAt)
	nextDue, enabled := int64(0), false
	if scheduled && routine.ScheduleKind == ScheduleCron && routine.Enabled && !routine.Deleted {
		var config struct {
			Cron     string `json:"cron"`
			Timezone string `json:"timezone"`
		}
		if err := json.Unmarshal([]byte(routine.ScheduleConfigJSON), &config); err != nil {
			return err
		}
		_, nextDue, err = encodeSchedule(Schedule{Kind: ScheduleCron, Cron: config.Cron, Timezone: config.Timezone}, s.now())
		if err != nil {
			return err
		}
		enabled = true
	}
	_, err = s.store.FinishRoutineRun(ctx, run.ID, runState, errorText, reply, s.now().UnixMilli(), nextDue, enabled, scheduled)
	return err
}
