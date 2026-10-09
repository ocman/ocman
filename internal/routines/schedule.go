package routines

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/robfig/cron/v3"
)

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
	sessionMode := strings.TrimSpace(input.SessionMode)
	if sessionMode == "" {
		sessionMode = SessionNew
	}
	sessionID := strings.TrimSpace(input.SessionID)
	switch sessionMode {
	case SessionNew, SessionReuse:
		sessionID = ""
	case SessionExisting:
		if sessionID == "" {
			return state.Routine{}, fmt.Errorf("an existing session is required: %w", ErrValidation)
		}
	default:
		return state.Routine{}, fmt.Errorf("invalid session mode: %w", ErrValidation)
	}
	if input.Worktree && sessionMode != SessionNew {
		return state.Routine{}, fmt.Errorf("worktrees require a new session per run: %w", ErrValidation)
	}
	if input.CleanupWorktree && !input.Worktree {
		return state.Routine{}, fmt.Errorf("cleanup requires a routine worktree: %w", ErrValidation)
	}
	var config string
	var due int64
	if !input.KeepSchedule {
		var err error
		if config, due, err = encodeSchedule(input.Schedule, now); err != nil {
			return state.Routine{}, fmt.Errorf("invalid schedule: %w", ErrValidation)
		}
	}
	rules := input.PermissionRules
	if rules == nil {
		rules = []platforms.PermissionRule{}
	}
	rulesJSON, err := json.Marshal(rules)
	if err != nil {
		return state.Routine{}, fmt.Errorf("invalid permission rules: %w", ErrValidation)
	}
	return state.Routine{
		Name: name, Prompt: input.Prompt, Directory: directory, RemoteID: remoteID,
		Agent: strings.TrimSpace(input.Agent), Model: strings.TrimSpace(input.Model),
		SessionMode: sessionMode, SessionID: sessionID,
		Worktree: input.Worktree, CleanupWorktree: input.CleanupWorktree,
		ScheduleKind: input.Schedule.Kind, ScheduleConfigJSON: config, NextDueAt: due,
		Enabled: input.Enabled, DeleteAfterSuccess: input.DeleteAfterSuccess,
		ArchiveSessionAfterSuccess: input.ArchiveSessionAfterSuccess,
		NotifyOnSuccess:            input.NotifyOnSuccess,
		PermissionRulesJSON:        string(rulesJSON),
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

// InputFromRoutine turns a stored routine back into an Input, so a caller can
// change a few fields and Update the rest unchanged. A pending timeout keeps
// its remaining delay, and therefore its original due time.
func InputFromRoutine(routine state.Routine, now time.Time) (Input, error) {
	var config struct {
		DueAt    int64  `json:"dueAt"`
		At       int64  `json:"at"`
		Cron     string `json:"cron"`
		Timezone string `json:"timezone"`
	}
	if err := json.Unmarshal([]byte(routine.ScheduleConfigJSON), &config); err != nil && routine.ScheduleConfigJSON != "" {
		return Input{}, fmt.Errorf("stored schedule is unreadable: %w", ErrValidation)
	}
	var rules []platforms.PermissionRule
	if routine.PermissionRulesJSON != "" {
		if err := json.Unmarshal([]byte(routine.PermissionRulesJSON), &rules); err != nil {
			return Input{}, fmt.Errorf("stored permission rules are unreadable: %w", ErrValidation)
		}
	}
	schedule := Schedule{Kind: routine.ScheduleKind, Cron: config.Cron, Timezone: config.Timezone}
	if config.At > 0 {
		schedule.At = time.UnixMilli(config.At)
	}
	if routine.ScheduleKind == ScheduleTimeout {
		schedule.Timeout = time.UnixMilli(config.DueAt).Sub(now)
	}
	return Input{
		Name: routine.Name, Prompt: routine.Prompt, Directory: routine.Directory, RemoteID: routine.RemoteID,
		Agent: routine.Agent, Model: routine.Model, SessionMode: routine.SessionMode, SessionID: routine.SessionID,
		Worktree: routine.Worktree, CleanupWorktree: routine.CleanupWorktree,
		Schedule: schedule, Enabled: routine.Enabled, DeleteAfterSuccess: routine.DeleteAfterSuccess,
		ArchiveSessionAfterSuccess: routine.ArchiveSessionAfterSuccess, NotifyOnSuccess: routine.NotifyOnSuccess, PermissionRules: rules,
	}, nil
}
