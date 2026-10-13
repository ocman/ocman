package queuemod

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/NoUseFreak/ocman/internal/httpx"
	"github.com/NoUseFreak/ocman/internal/sessionsvc"
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

func (s *Module) Settings(w http.ResponseWriter, r *http.Request) {
	if s.d.Store == nil {
		http.Error(w, "state database not available", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		m, err := s.getModelFallthroughSettings(r.Context())
		if err != nil {
			httpx.ServerError(w, "reading model fallthrough settings", err)
			return
		}
		httpx.WriteJSON(w, m)
	case http.MethodPost:
		var m modelFallthroughSettings
		if !httpx.ReadJSON(w, r, httpx.MaxRequestBody, &m) {
			return
		}
		if err := m.validate(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		raw, _ := json.Marshal(m)
		if err := s.d.Store.SetSetting(r.Context(), modelFallthroughSettingKey, string(raw)); err != nil {
			httpx.ServerError(w, "saving model fallthrough settings", err)
			return
		}
		httpx.WriteJSON(w, m)
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
