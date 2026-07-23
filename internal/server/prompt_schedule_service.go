package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

const (
	StateScheduled = "scheduled"
	StateRunning   = "running"
	StateCompleted = "completed"
	StateFailed    = "failed"
	StateCanceled  = "canceled"
)

var (
	ErrInvalidState = errors.New("prompt schedule is not scheduled")
	ErrValidation   = errors.New("invalid prompt schedule")
)

type Store interface {
	CreatePromptSchedule(state.PromptSchedule) error
	ListPromptSchedules(string) ([]state.PromptSchedule, error)
	GetPromptSchedule(string) (state.PromptSchedule, error)
	ClaimPromptSchedule(string, int64, bool) (state.PromptSchedule, bool, error)
	ClaimNextDuePromptSchedule(int64) (state.PromptSchedule, bool, error)
	CancelPromptSchedule(string, int64) (state.PromptSchedule, bool, error)
	LinkPromptScheduleSession(string, string, string, int64) error
	FinishPromptSchedule(string, string, string, int64) error
	FailRunningPromptSchedules(int64, string) error
}

type Sessions interface {
	CreateScheduledSession(context.Context, string) (string, *platforms.CreateSessionResponse, error)
	SendScheduledMessage(context.Context, string, platforms.SendMessageRequest) error
}

type promptScheduleService struct {
	store    Store
	sessions Sessions
	now      func() time.Time
	newID    func() string
}

type promptScheduleCreateRequest struct {
	Directory string `json:"directory"`
	Prompt    string `json:"prompt"`
	RunAt     int64  `json:"runAt"`
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
	now := s.now().UnixMilli()
	if req.Directory == "" || req.Prompt == "" || req.RunAt <= now {
		return state.PromptSchedule{}, fmt.Errorf("directory, prompt, and a future runAt are required: %w", ErrValidation)
	}
	schedule := state.PromptSchedule{ID: s.newID(), Directory: req.Directory, Prompt: req.Prompt, RunAt: req.RunAt, State: StateScheduled, CreatedAt: now, UpdatedAt: now}
	return schedule, s.store.CreatePromptSchedule(schedule)
}

func (s *promptScheduleService) List(_ context.Context, directory string) ([]state.PromptSchedule, error) {
	if directory == "" {
		return nil, fmt.Errorf("directory is required: %w", ErrValidation)
	}
	return s.store.ListPromptSchedules(directory)
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
	return s.store.FailRunningPromptSchedules(s.now().UnixMilli(), "dispatch interrupted by restart; not retried to prevent duplicate session creation")
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
	platformID, resp, err := s.sessions.CreateScheduledSession(ctx, schedule.Directory)
	if err != nil {
		return s.finishFailed(schedule.ID, err)
	}
	now := s.now().UnixMilli()
	if err := s.store.LinkPromptScheduleSession(schedule.ID, platformID, resp.ID, now); err != nil {
		return fmt.Errorf("linking scheduled session: %w", err)
	}
	if err := s.sessions.SendScheduledMessage(ctx, platformID, platforms.SendMessageRequest{SessionID: resp.ID, Message: schedule.Prompt}); err != nil {
		return s.finishFailed(schedule.ID, err)
	}
	return s.store.FinishPromptSchedule(schedule.ID, StateCompleted, "", s.now().UnixMilli())
}

func (s *promptScheduleService) finishFailed(id string, cause error) error {
	if err := s.store.FinishPromptSchedule(id, StateFailed, cause.Error(), s.now().UnixMilli()); err != nil {
		return fmt.Errorf("recording schedule failure after %w: %w", cause, err)
	}
	return nil
}
