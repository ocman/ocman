package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/robfig/cron/v3"
)

const (
	StateScheduled   = "scheduled"
	StateRunning     = "running"
	StateCompleted   = "completed"
	StateFailed      = "failed"
	StateCanceled    = "canceled"
	TimingOnce       = "once"
	TimingInterval   = "interval"
	TimingCron       = "cron"
	SessionModeFresh = "fresh"
	SessionModeReuse = "reuse"
)

var (
	ErrInvalidState = errors.New("prompt schedule is not scheduled")
	ErrValidation   = errors.New("invalid prompt schedule")
)

type Store interface {
	CreatePromptSchedule(state.PromptSchedule) error
	ListPromptSchedules(string, string) ([]state.PromptSchedule, error)
	ListRunningPromptSchedules() ([]state.PromptSchedule, error)
	GetPromptSchedule(string) (state.PromptSchedule, error)
	ClaimPromptSchedule(string, int64, bool) (state.PromptSchedule, bool, error)
	ClaimNextDuePromptSchedule(int64) (state.PromptSchedule, bool, error)
	CancelPromptSchedule(string, int64) (state.PromptSchedule, bool, error)
	LinkPromptScheduleSession(string, string, string, int64) error
	FinishPromptSchedule(string, string, string, int64) error
	CompletePromptSchedule(string, int64, int64) error
	ReschedulePromptSchedule(string, int64, string, int64) error
	SetPromptScheduleEnabled(string, bool, int64, int64) (state.PromptSchedule, error)
}

type Sessions interface {
	CreateScheduledSession(context.Context, string, string) (string, *platforms.CreateSessionResponse, error)
	SendScheduledMessage(context.Context, string, platforms.SendMessageRequest, bool) error
}

type promptScheduleService struct {
	store    Store
	sessions Sessions
	now      func() time.Time
	newID    func() string
}

type promptScheduleCreateRequest struct {
	Directory       string `json:"directory"`
	RemoteID        string `json:"remoteId"`
	Prompt          string `json:"prompt"`
	RunAt           int64  `json:"runAt"`
	TimingType      string `json:"timingType"`
	IntervalMinutes int64  `json:"intervalMinutes"`
	Cron            string `json:"cron"`
	Timezone        string `json:"timezone"`
	Enabled         *bool  `json:"enabled"`
	SessionMode     string `json:"sessionMode"`
}

func newPromptScheduleService(store Store, sessions Sessions, now func() time.Time, newID func() string) *promptScheduleService {
	if now == nil {
		now = time.Now
	}
	if newID == nil {
		newID = scheduleID
	}
	return &promptScheduleService{store: store, sessions: sessions, now: now, newID: newID}
}

func scheduleID() string {
	var raw [8]byte
	_, _ = rand.Read(raw[:])
	return "ps_" + hex.EncodeToString(raw[:])
}

func (s *promptScheduleService) Create(_ context.Context, req promptScheduleCreateRequest) (state.PromptSchedule, error) {
	now := s.now()
	if req.Directory == "" || req.Prompt == "" {
		return state.PromptSchedule{}, fmt.Errorf("directory and prompt are required: %w", ErrValidation)
	}
	if req.RemoteID == "" {
		req.RemoteID = "local"
	}
	if req.TimingType == "" {
		req.TimingType = TimingOnce
	}
	if req.Timezone == "" {
		req.Timezone = "UTC"
	}
	if req.SessionMode == "" {
		req.SessionMode = SessionModeFresh
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	runAt, err := nextScheduleRun(req.TimingType, req.RunAt, req.IntervalMinutes, req.Cron, req.Timezone, now)
	if err != nil || (req.SessionMode != SessionModeFresh && req.SessionMode != SessionModeReuse) {
		return state.PromptSchedule{}, fmt.Errorf("invalid timing, timezone, or session mode: %w", ErrValidation)
	}
	nowMS := now.UnixMilli()
	schedule := state.PromptSchedule{ID: s.newID(), Directory: req.Directory, RemoteID: req.RemoteID, Prompt: req.Prompt,
		RunAt: runAt, TimingType: req.TimingType, IntervalMinutes: req.IntervalMinutes, Cron: req.Cron,
		Timezone: req.Timezone, Enabled: enabled, SessionMode: req.SessionMode, State: StateScheduled, CreatedAt: nowMS, UpdatedAt: nowMS}
	return schedule, s.store.CreatePromptSchedule(schedule)
}

func nextScheduleRun(timing string, runAt, intervalMinutes int64, expression, timezone string, after time.Time) (int64, error) {
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return 0, err
	}
	switch timing {
	case TimingOnce:
		if runAt <= after.UnixMilli() {
			return 0, ErrValidation
		}
		return runAt, nil
	case TimingInterval:
		if intervalMinutes <= 0 || intervalMinutes > math.MaxInt64/int64(time.Minute) {
			return 0, ErrValidation
		}
		return after.Add(time.Duration(intervalMinutes) * time.Minute).UnixMilli(), nil
	case TimingCron:
		if len(strings.Fields(expression)) != 5 {
			return 0, ErrValidation
		}
		schedule, err := cron.ParseStandard(expression)
		if err != nil {
			return 0, err
		}
		return schedule.Next(after.In(location)).UnixMilli(), nil
	default:
		return 0, ErrValidation
	}
}

func (s *promptScheduleService) List(_ context.Context, directory, remoteID string) ([]state.PromptSchedule, error) {
	if directory == "" {
		return nil, fmt.Errorf("directory is required: %w", ErrValidation)
	}
	if remoteID == "" {
		remoteID = "local"
	}
	return s.store.ListPromptSchedules(directory, remoteID)
}

func (s *promptScheduleService) Get(_ context.Context, id string) (state.PromptSchedule, error) {
	return s.store.GetPromptSchedule(id)
}

func (s *promptScheduleService) Cancel(_ context.Context, id string) (state.PromptSchedule, error) {
	schedule, ok, err := s.store.CancelPromptSchedule(id, s.now().UnixMilli())
	if err != nil {
		return schedule, err
	}
	if !ok {
		return schedule, fmt.Errorf("only scheduled prompts can be canceled: %w", ErrInvalidState)
	}
	return schedule, nil
}

func (s *promptScheduleService) SetEnabled(_ context.Context, id string, enabled bool) (state.PromptSchedule, error) {
	schedule, err := s.store.GetPromptSchedule(id)
	if err != nil {
		return schedule, err
	}
	runAt := schedule.RunAt
	if enabled && (schedule.TimingType == TimingInterval || schedule.TimingType == TimingCron) {
		runAt, err = nextScheduleRun(schedule.TimingType, schedule.RunAt, schedule.IntervalMinutes, schedule.Cron, schedule.Timezone, s.now())
		if err != nil {
			return schedule, err
		}
	}
	return s.store.SetPromptScheduleEnabled(id, enabled, runAt, s.now().UnixMilli())
}

func (s *promptScheduleService) RunNow(ctx context.Context, id string) (state.PromptSchedule, error) {
	schedule, ok, err := s.store.ClaimPromptSchedule(id, s.now().UnixMilli(), true)
	if err != nil {
		return schedule, err
	}
	if !ok {
		return schedule, fmt.Errorf("only scheduled prompts can run now: %w", ErrInvalidState)
	}
	if err := s.dispatch(ctx, schedule); err != nil {
		return state.PromptSchedule{}, err
	}
	return s.store.GetPromptSchedule(id)
}

func (s *promptScheduleService) Recover() error {
	const interrupted = "dispatch interrupted by restart; not retried to prevent duplicate session creation"
	schedules, err := s.store.ListRunningPromptSchedules()
	if err != nil {
		return err
	}
	now := s.now()
	var recoveryErr error
	for _, schedule := range schedules {
		if schedule.TimingType == TimingOnce {
			recoveryErr = errors.Join(recoveryErr, s.finishFailed(schedule.ID, errors.New(interrupted)))
			continue
		}
		next, err := nextScheduleRun(schedule.TimingType, schedule.RunAt, schedule.IntervalMinutes, schedule.Cron, schedule.Timezone, now)
		if err != nil {
			recoveryErr = errors.Join(recoveryErr, s.finishFailed(schedule.ID, err))
			continue
		}
		recoveryErr = errors.Join(recoveryErr, s.store.ReschedulePromptSchedule(schedule.ID, next, interrupted, now.UnixMilli()))
	}
	return recoveryErr
}

func (s *promptScheduleService) Tick(ctx context.Context) error {
	for {
		schedule, ok, err := s.store.ClaimNextDuePromptSchedule(s.now().UnixMilli())
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		if err := s.dispatch(ctx, schedule); err != nil {
			return err
		}
	}
}

func (s *promptScheduleService) dispatch(ctx context.Context, schedule state.PromptSchedule) error {
	platformID, sessionID := schedule.Platform, schedule.SessionID
	reusing := schedule.SessionMode == SessionModeReuse && sessionID != ""
	if !reusing {
		createdPlatform, resp, err := s.sessions.CreateScheduledSession(ctx, schedule.RemoteID, schedule.Directory)
		if err != nil {
			return s.finishDispatchError(schedule, err)
		}
		platformID, sessionID = createdPlatform, resp.ID
		if err := s.store.LinkPromptScheduleSession(schedule.ID, platformID, sessionID, s.now().UnixMilli()); err != nil {
			return s.finishDispatchError(schedule, fmt.Errorf("linking scheduled session: %w", err))
		}
	}
	if err := s.sessions.SendScheduledMessage(ctx, platformID, platforms.SendMessageRequest{SessionID: sessionID, Message: schedule.Prompt}, reusing); err != nil {
		return s.finishDispatchError(schedule, err)
	}
	if schedule.TimingType != TimingOnce {
		next, err := nextScheduleRun(schedule.TimingType, schedule.RunAt, schedule.IntervalMinutes, schedule.Cron, schedule.Timezone, s.now())
		if err != nil {
			return s.finishFailed(schedule.ID, err)
		}
		return s.store.CompletePromptSchedule(schedule.ID, next, s.now().UnixMilli())
	}
	return s.store.FinishPromptSchedule(schedule.ID, StateCompleted, "", s.now().UnixMilli())
}

func (s *promptScheduleService) finishDispatchError(schedule state.PromptSchedule, cause error) error {
	if errors.Is(cause, context.Canceled) {
		return cause
	}
	if schedule.TimingType == TimingOnce {
		return s.finishFailed(schedule.ID, cause)
	}
	now := s.now()
	next, err := nextScheduleRun(schedule.TimingType, schedule.RunAt, schedule.IntervalMinutes, schedule.Cron, schedule.Timezone, now)
	if err != nil {
		return s.finishFailed(schedule.ID, err)
	}
	if err := s.store.ReschedulePromptSchedule(schedule.ID, next, cause.Error(), now.UnixMilli()); err != nil {
		return fmt.Errorf("rescheduling after %w: %w", cause, err)
	}
	return nil
}

func (s *promptScheduleService) finishFailed(id string, cause error) error {
	if err := s.store.FinishPromptSchedule(id, StateFailed, cause.Error(), s.now().UnixMilli()); err != nil {
		return fmt.Errorf("recording schedule failure after %w: %w", cause, err)
	}
	return nil
}
