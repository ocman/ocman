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
	config, due, err := encodeSchedule(input.Schedule, now)
	if err != nil {
		return state.Routine{}, fmt.Errorf("invalid schedule: %w", ErrValidation)
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
		ScheduleKind: input.Schedule.Kind, ScheduleConfigJSON: config, NextDueAt: due,
		Enabled: input.Enabled, DeleteAfterSuccess: input.DeleteAfterSuccess,
		ArchiveSessionAfterSuccess: input.ArchiveSessionAfterSuccess,
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
