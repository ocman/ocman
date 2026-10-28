package queuemod

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ocman/ocman/internal/httpx"
	"github.com/ocman/ocman/internal/sessionsvc"
)

const modelFallthroughSettingKey = "model_fallthrough"
const maxFallthroughMinutes = 24 * 60

type modelFallthroughSettings struct {
	PatienceMinutes int `json:"patienceMinutes"`
	FallbackMinutes int `json:"fallbackMinutes"`
}

func (m modelFallthroughSettings) validate() error {
	if m.PatienceMinutes < 1 || m.PatienceMinutes > maxFallthroughMinutes || m.FallbackMinutes < 1 || m.FallbackMinutes > maxFallthroughMinutes {
		return fmt.Errorf("minutes must be between 1 and %d", maxFallthroughMinutes)
	}
	return nil
}

func (s *Module) getModelFallthroughSettings(ctx context.Context) (modelFallthroughSettings, error) {
	m := modelFallthroughSettings{int(sessionsvc.DefaultPatience / time.Minute), int(sessionsvc.DefaultCooldownFallback / time.Minute)}
	if s.d.Store == nil {
		return m, errors.New("state database not available")
	}
	raw, ok, err := s.d.Store.GetSetting(ctx, modelFallthroughSettingKey)
	if err != nil || !ok {
		return m, err
	}
	var stored modelFallthroughSettings
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		return m, fmt.Errorf("decoding model fallthrough settings: %w", err)
	}
	if err := stored.validate(); err != nil {
		return m, err
	}
	return stored, nil
}

func (s *Module) CooldownTimes(ctx context.Context) (time.Duration, time.Duration) {
	m, _ := s.getModelFallthroughSettings(ctx)
	return time.Duration(m.PatienceMinutes) * time.Minute, time.Duration(m.FallbackMinutes) * time.Minute
}

// Settings serves /api/settings/model-fallthrough.
func (s *Module) Settings() httpx.SettingRoute[modelFallthroughSettings] {
	return httpx.SettingRoute[modelFallthroughSettings]{
		Name:      "model fallthrough settings",
		Available: func() bool { return s.d.Store != nil },
		Get: func(ctx context.Context) (any, error) {
			return s.getModelFallthroughSettings(ctx)
		},
		Validate: func(m *modelFallthroughSettings) error { return m.validate() },
		Set: func(ctx context.Context, m modelFallthroughSettings) error {
			raw, _ := json.Marshal(m)
			return s.d.Store.SetSetting(ctx, modelFallthroughSettingKey, string(raw))
		},
	}
}
