package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/NoUseFreak/ocman/internal/httpx"
)

const (
	autoArchiveSettingKey     = "auto_archive"
	defaultAutoArchiveTTLDays = 7
	maximumAutoArchiveTTLDays = 3650
)

type autoArchiveSettingsView struct {
	Enabled bool `json:"enabled"`
	TTLDays int  `json:"ttlDays"`
}

type worktreeInheritBody struct {
	Enabled bool `json:"enabled"`
}

func (s *Server) hasStateDB() bool { return s.stateDB != nil }

// worktreeInheritSetting serves /api/settings/worktree-inherit-permissions
// (issue #101): {"enabled": bool}, enabled when unset.
func (s *Server) worktreeInheritSetting() httpx.SettingRoute[worktreeInheritBody] {
	return httpx.SettingRoute[worktreeInheritBody]{
		Name:      "worktree inherit-permissions setting",
		Available: s.hasStateDB,
		Get: func(ctx context.Context) (any, error) {
			on, err := s.stateDB.GetWorktreeInheritPermissions(ctx)
			return worktreeInheritBody{on}, err
		},
		Set: func(ctx context.Context, body worktreeInheritBody) error {
			return s.stateDB.SetWorktreeInheritPermissions(ctx, body.Enabled)
		},
	}
}

func (s *Server) autoArchiveSetting() httpx.SettingRoute[autoArchiveSettingsView] {
	return httpx.SettingRoute[autoArchiveSettingsView]{
		Name:      "auto-archive settings",
		Available: s.hasStateDB,
		Get: func(ctx context.Context) (any, error) {
			return s.getAutoArchiveSettings(ctx)
		},
		Validate: func(v *autoArchiveSettingsView) error { return validateAutoArchiveSettings(*v) },
		Set:      s.setAutoArchiveSettings,
	}
}

func (s *Server) getAutoArchiveSettings(ctx context.Context) (autoArchiveSettingsView, error) {
	settings := autoArchiveSettingsView{Enabled: true, TTLDays: defaultAutoArchiveTTLDays}
	if s.stateDB == nil {
		return settings, errors.New("state database not available")
	}
	value, ok, err := s.stateDB.GetSetting(ctx, autoArchiveSettingKey)
	if err != nil || !ok {
		return settings, err
	}
	if err := json.Unmarshal([]byte(value), &settings); err != nil {
		return settings, fmt.Errorf("decode auto-archive settings: %w", err)
	}
	if err := validateAutoArchiveSettings(settings); err != nil {
		return settings, err
	}
	return settings, nil
}

func (s *Server) setAutoArchiveSettings(ctx context.Context, settings autoArchiveSettingsView) error {
	if err := validateAutoArchiveSettings(settings); err != nil {
		return err
	}
	value, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	return s.stateDB.SetSetting(ctx, autoArchiveSettingKey, string(value))
}

func validateAutoArchiveSettings(settings autoArchiveSettingsView) error {
	if settings.TTLDays < 1 || settings.TTLDays > maximumAutoArchiveTTLDays {
		return fmt.Errorf("ttlDays must be between 1 and %d", maximumAutoArchiveTTLDays)
	}
	return nil
}
