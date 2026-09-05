package routines

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/remote"
	"github.com/NoUseFreak/ocman/internal/sessionsvc"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/robfig/cron/v3"
)

const (
	ScheduleNone    = "none"
	ScheduleTimeout = "timeout"
	ScheduleOnce    = "once"
	ScheduleCron    = "cron"

	RunRunning = "running"
	RunSuccess = "success"
	RunFailure = "failure"
)

var (
	ErrValidation   = errors.New("invalid routine")
	ErrNameConflict = errors.New("routine name already exists")
)

type Schedule struct {
	Kind     string
	Timeout  time.Duration
	At       time.Time
	Cron     string
	Timezone string
}

type Input struct {
	Name               string
	Prompt             string
	Directory          string
	RemoteID           string
	Schedule           Schedule
	Enabled            bool
	DeleteAfterSuccess bool
}

type Deps struct {
	Store     *state.DB
	Router    *hostsvc.Router
	Sessions  *sessionsvc.Service
	Platforms *platforms.Registry
	Now       func() time.Time
	NewID     func(prefix string) string
}

type Service struct {
	store     *state.DB
	router    *hostsvc.Router
	sessions  *sessionsvc.Service
	platforms *platforms.Registry
	now       func() time.Time
	newID     func(string) string
}

func New(deps Deps) *Service {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.NewID == nil {
		deps.NewID = randomID
	}
	return &Service{store: deps.Store, router: deps.Router, sessions: deps.Sessions, platforms: deps.Platforms, now: deps.Now, newID: deps.NewID}
}

func randomID(prefix string) string {
	var raw [8]byte
	_, _ = rand.Read(raw[:])
	return prefix + hex.EncodeToString(raw[:])
}

func (s *Service) Create(ctx context.Context, input Input) (state.Routine, error) {
	now := s.now()
	routine, err := buildRoutine(input, now)
	if err != nil {
		return state.Routine{}, err
	}
	routine.ID = s.newID("routine-")
	routine.CreatedAt, routine.UpdatedAt = now.UnixMilli(), now.UnixMilli()
	if err := s.store.CreateRoutine(ctx, routine); err != nil {
		return state.Routine{}, normalizeWriteError(err)
	}
	return routine, nil
}

func (s *Service) Update(ctx context.Context, id string, input Input) (state.Routine, error) {
	existing, err := s.store.GetRoutine(ctx, id)
	if err != nil {
		return state.Routine{}, err
	}
	now := s.now()
	routine, err := buildRoutine(input, now)
	if err != nil {
		return state.Routine{}, err
	}
	routine.ID, routine.CreatedAt, routine.UpdatedAt = id, existing.CreatedAt, now.UnixMilli()
	if err := s.store.UpdateRoutine(ctx, routine); err != nil {
		return state.Routine{}, normalizeWriteError(err)
	}
	return s.store.GetRoutine(ctx, id)
}

func (s *Service) Get(ctx context.Context, id string) (state.Routine, error) {
	return s.store.GetRoutine(ctx, id)
}

func (s *Service) List(ctx context.Context, includeDeleted bool) ([]state.Routine, error) {
	return s.store.ListRoutines(ctx, includeDeleted)
}

func (s *Service) Delete(ctx context.Context, id string) error {
	return s.store.SoftDeleteRoutine(ctx, id, s.now().UnixMilli())
}

func (s *Service) History(ctx context.Context, id string) ([]state.RoutineRun, error) {
	if _, err := s.store.GetRoutine(ctx, id); err != nil {
		return nil, err
	}
	return s.store.ListRoutineRuns(ctx, id)
}

func normalizeWriteError(err error) error {
	if strings.Contains(err.Error(), "UNIQUE constraint failed: routine.name") {
		return fmt.Errorf("%w: %v", ErrNameConflict, err)
	}
	return err
}

func buildRoutine(input Input, now time.Time) (state.Routine, error) {
	name := strings.TrimSpace(input.Name)
	directory := strings.TrimSpace(input.Directory)
	if name == "" || strings.TrimSpace(input.Prompt) == "" || directory == "" || !filepath.IsAbs(directory) {
		return state.Routine{}, fmt.Errorf("name, prompt, and an absolute target directory are required: %w", ErrValidation)
	}
	remoteID := strings.TrimSpace(input.RemoteID)
	if remoteID == "" {
		remoteID = "local"
	}
	config, due, err := encodeSchedule(input.Schedule, now)
	if err != nil {
		return state.Routine{}, fmt.Errorf("invalid schedule: %w", ErrValidation)
	}
	return state.Routine{
		Name: name, Prompt: input.Prompt, Directory: directory, RemoteID: remoteID,
		ScheduleKind: input.Schedule.Kind, ScheduleConfigJSON: config, NextDueAt: due,
		Enabled: input.Enabled, DeleteAfterSuccess: input.DeleteAfterSuccess,
	}, nil
}

func encodeSchedule(schedule Schedule, now time.Time) (string, int64, error) {
	switch schedule.Kind {
	case ScheduleNone:
		return `{}`, 0, nil
	case ScheduleTimeout:
		if schedule.Timeout <= 0 || schedule.Timeout.Milliseconds() <= 0 {
			return "", 0, ErrValidation
		}
		due := now.Add(schedule.Timeout).UnixMilli()
		config, _ := json.Marshal(struct {
			DueAt int64 `json:"dueAt"`
		}{due})
		return string(config), due, nil
	case ScheduleOnce:
		if schedule.At.IsZero() || !schedule.At.After(now) {
			return "", 0, ErrValidation
		}
		due := schedule.At.UnixMilli()
		config, _ := json.Marshal(struct {
			At int64 `json:"at"`
		}{due})
		return string(config), due, nil
	case ScheduleCron:
		zone := schedule.Timezone
		if zone == "" {
			zone = "UTC"
		}
		location, err := time.LoadLocation(zone)
		if err != nil || len(strings.Fields(schedule.Cron)) != 5 {
			return "", 0, ErrValidation
		}
		parsed, err := cron.ParseStandard(schedule.Cron)
		if err != nil {
			return "", 0, err
		}
		config, _ := json.Marshal(struct {
			Cron     string `json:"cron"`
			Timezone string `json:"timezone"`
		}{schedule.Cron, zone})
		return string(config), parsed.Next(now.In(location)).UnixMilli(), nil
	default:
		return "", 0, ErrValidation
	}
}

func (s *Service) RunNow(ctx context.Context, routineID string) (state.RoutineRun, error) {
	routine, err := s.store.GetRoutine(ctx, routineID)
	if err != nil {
		return state.RoutineRun{}, err
	}
	occurrence := s.now().UnixMilli()
	if routine.NextDueAt > 0 && routine.NextDueAt <= occurrence {
		occurrence = routine.NextDueAt
	}
	return s.claimAndDispatch(ctx, routine, occurrence, "manual")
}

func (s *Service) Tick(ctx context.Context) error {
	if err := s.settleRunning(ctx, false); err != nil {
		return err
	}
	routines, err := s.store.ListDueRoutines(ctx, s.now().UnixMilli())
	if err != nil {
		return err
	}
	for _, routine := range routines {
		if _, err := s.claimAndDispatch(ctx, routine, routine.NextDueAt, "schedule"); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) Recover(ctx context.Context) error {
	return s.settleRunning(ctx, true)
}

func (s *Service) claimAndDispatch(ctx context.Context, routine state.Routine, occurrence int64, trigger string) (state.RoutineRun, error) {
	now := s.now().UnixMilli()
	run, claimed, err := s.store.ClaimRoutineRun(ctx, state.RoutineRun{
		ID: s.newID("run-"), RoutineID: routine.ID, Trigger: trigger, State: RunRunning,
		OccurrenceAt: occurrence, CreatedAt: now,
	})
	if err != nil || !claimed {
		return run, err
	}
	host, ok := s.router.LookupRemote(run.RemoteID)
	if !ok {
		return s.failDispatch(ctx, run, fmt.Errorf("routine target %q is unavailable", run.RemoteID))
	}
	ensured, err := host.EnsureProjectOpencode(ctx, hostsvc.EnsureProjectOpencodeRequest{ProjectDir: run.Directory})
	if err != nil {
		return s.failDispatch(ctx, run, err)
	}
	platformID := "opencode"
	if host.RemoteID() != "" && host.RemoteID() != "local" {
		platformID = remote.CompoundPlatformID(host.RemoteID(), platformID)
	}
	created, err := s.sessions.Create(ctx, platformID, platforms.CreateSessionRequest{Directory: run.Directory, Port: ensured.Port()})
	if err != nil {
		return s.failDispatch(ctx, run, err)
	}
	if err := s.sessions.SendMessage(ctx, platformID, platforms.SendMessageRequest{SessionID: created.ID, Message: run.Prompt}); err != nil {
		return s.failDispatch(ctx, run, err)
	}
	if err := s.store.LinkRoutineRun(ctx, run.ID, platformID, created.ID, s.now().UnixMilli()); err != nil {
		return s.failDispatch(ctx, run, fmt.Errorf("linking routine session: %w", err))
	}
	return s.store.GetRoutineRun(ctx, run.ID)
}

func (s *Service) failDispatch(ctx context.Context, run state.RoutineRun, cause error) (state.RoutineRun, error) {
	if err := s.finish(ctx, run, RunFailure, cause.Error()); err != nil {
		return state.RoutineRun{}, fmt.Errorf("recording routine failure after %w: %w", cause, err)
	}
	return s.store.GetRoutineRun(ctx, run.ID)
}

func (s *Service) settleRunning(ctx context.Context, recoverOrphans bool) error {
	runs, err := s.store.ListRunningRoutineRuns(ctx)
	if err != nil {
		return err
	}
	var result error
	for _, run := range runs {
		if run.Platform == "" || run.SessionID == "" {
			if recoverOrphans {
				result = errors.Join(result, s.finish(ctx, run, RunFailure, "dispatch interrupted before session linkage"))
			}
			continue
		}
		platform, ok := s.platforms.Get(platforms.ID(run.Platform))
		if !ok {
			continue
		}
		detail, err := platform.Session(ctx, run.SessionID, 1, 0)
		if err != nil || detail == nil || detail.Session == nil {
			continue
		}
		switch detail.Session.Status {
		case db.StatusDone:
			result = errors.Join(result, s.finish(ctx, run, RunSuccess, ""))
		case db.StatusError, db.StatusInterrupted:
			result = errors.Join(result, s.finish(ctx, run, RunFailure, detail.Session.Status.String()))
		}
	}
	return result
}

func (s *Service) finish(ctx context.Context, run state.RoutineRun, runState, errorText string) error {
	routine, err := s.store.GetRoutine(ctx, run.RoutineID)
	if err != nil {
		return err
	}
	nextDue, enabled := int64(0), false
	if routine.ScheduleKind == ScheduleCron && routine.Enabled && !routine.Deleted {
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
	_, err = s.store.FinishRoutineRun(ctx, run.ID, runState, errorText, s.now().UnixMilli(), nextDue, enabled)
	return err
}
