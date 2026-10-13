// Package snooze owns conversation snoozing and the deadline wake-up worker.
// Archive persistence remains in state, shared with manual and auto-archive.
package snooze

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/NoUseFreak/ocman/internal/httpx"
	"github.com/NoUseFreak/ocman/internal/module"
	"github.com/NoUseFreak/ocman/internal/state"
	log "github.com/sirupsen/logrus"
)

type Deps struct {
	Store *state.DB
	// Validate resolves the session's platform and writes any rejection.
	Validate  func(http.ResponseWriter, *http.Request, string, string) (string, bool)
	Broadcast func(string, []byte)
	Guards    module.Guards
}

// Module has no mutable state; routes and workers share its durable store.
type Module struct{ d Deps }

var _ module.Module = (*Module)(nil)
var _ module.Runner = (*Module)(nil)

func NewModule(d Deps) *Module { return &Module{d: d} }
func (*Module) Name() string   { return "snooze" }

func (m *Module) Routes(r module.Router) {
	r.Handle("POST /api/session/snooze", m.d.Guards.Auth(m.handleSnooze))
}

func (m *Module) handleSnooze(w http.ResponseWriter, r *http.Request) {
	if m.d.Store == nil {
		http.Error(w, "snooze unavailable", http.StatusServiceUnavailable)
		return
	}
	var req struct {
		Platform  string `json:"platform"`
		SessionID string `json:"sessionId"`
		Until     int64  `json:"until"`
	}
	if !httpx.ReadJSON(w, r, httpx.MaxRequestBody, &req) {
		return
	}
	if req.Until <= time.Now().UnixMilli() {
		http.Error(w, "snooze time must be in the future", http.StatusBadRequest)
		return
	}
	platform, ok := m.d.Validate(w, r, req.SessionID, req.Platform)
	if !ok {
		return
	}
	if err := m.d.Store.SnoozeSession(r.Context(), platform, req.SessionID, req.Until); err != nil {
		httpx.ServerError(w, "snoozing session", err)
		return
	}
	m.broadcast(platform, req.SessionID, true)
	httpx.WriteJSON(w, map[string]bool{"ok": true})
}

func (m *Module) broadcast(platform, sessionID string, archived bool) {
	payload, _ := json.Marshal(map[string]any{
		"sessionID": sessionID, "platform": platform,
		"patch": map[string]bool{"archived": archived},
	})
	m.d.Broadcast("ocman.session.changed", payload)
}

func (m *Module) Run(ctx context.Context) {
	m.run(ctx, time.Minute)
}

func (m *Module) run(ctx context.Context, interval time.Duration) {
	if m.d.Store == nil {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		m.wake(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (m *Module) wake(ctx context.Context) {
	keys, err := m.d.Store.WakeSnoozedSessions(ctx, time.Now().UnixMilli())
	if err != nil {
		if ctx.Err() == nil {
			log.WithError(err).Error("waking snoozed sessions")
		}
		return
	}
	for _, key := range keys {
		m.broadcast(key.Platform, key.SessionID, false)
	}
}
