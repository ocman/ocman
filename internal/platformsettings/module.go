// Package platformsettings persists the local platforms selected for startup.
package platformsettings

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/httpx"
	"github.com/ocman/ocman/internal/module"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/state"
)

const Default = "opencode,claudecode"
const setting = "platforms.enabled"

type Module struct {
	store               *state.DB
	mu                  sync.Mutex
	pending, overridden bool
	recoveryPending     bool
	active              []string
	reload              func()
	idle                func(context.Context) (bool, error)
	inFlight            int
	admitted            uint64 // Begin calls ever, so a reload sees one raced in
	setup               func() hostsvc.PlatformSetupManager
}

func New(store *state.DB) *Module { return &Module{store: store} }
func (m *Module) Name() string    { return "platform-settings" }
func (m *Module) Routes(r module.Router) {
	r.LocalSetting("/api/settings/platforms", m.settings)
	r.LocalSetting("GET /api/settings/platforms/{platform}/setup", m.checkSetup)
	r.LocalSetting("POST /api/settings/platforms/{platform}/install", m.installSetup)
}

func (m *Module) WithSetup(setup func() hostsvc.PlatformSetupManager) *Module {
	m.setup = setup
	return m
}

func (m *Module) Configure(reload func(), active []string, overridden bool, idle func(context.Context) (bool, error)) {
	m.reload, m.active, m.overridden, m.idle = reload, slices.Clone(active), overridden, idle
	slices.Sort(m.active)
}

type drainKey struct{}

// DrainContext lets previously queued work finish while new turns are fenced.
func DrainContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, drainKey{}, true)
}

func (m *Module) Begin(ctx context.Context) (func(), error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if (m.pending || m.recoveryPending) && ctx.Value(drainKey{}) != true {
		return nil, fmt.Errorf("platform configuration is reloading after active work finishes: %w", platforms.ErrBusy)
	}
	m.inFlight++
	m.admitted++
	return func() { m.mu.Lock(); m.inFlight--; m.mu.Unlock() }, nil
}

func (m *Module) Run(ctx context.Context) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if m.tryReload(ctx) {
				return
			}
		}
	}
}

// tryReload checks idleness without the lock (sends and Settings need it),
// then reloads only if nothing was admitted meanwhile.
func (m *Module) tryReload(ctx context.Context) bool {
	m.mu.Lock()
	if (!m.pending && !m.recoveryPending) || m.reload == nil || m.inFlight != 0 {
		m.mu.Unlock()
		return false
	}
	admitted := m.admitted
	m.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	idle, err := m.idle(ctx)
	if err != nil || !idle {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if (!m.pending && !m.recoveryPending) || m.inFlight != 0 || m.admitted != admitted {
		return false
	}
	m.reload()
	return true
}

func validate(enabled []string) error {
	if len(enabled) == 0 {
		return fmt.Errorf("enable at least one platform")
	}
	for i, id := range enabled {
		if id != "opencode" && id != "claudecode" && id != "codex" {
			return fmt.Errorf("unknown platform %q", id)
		}
		if slices.Contains(enabled[:i], id) {
			return fmt.Errorf("duplicate platform %q", id)
		}
	}
	return nil
}

func Load(ctx context.Context, store *state.DB) ([]string, error) {
	value, ok, err := store.GetSetting(ctx, setting)
	if err != nil {
		return nil, err
	}
	if !ok {
		return strings.Split(Default, ","), nil
	}
	enabled := strings.Split(value, ",")
	return enabled, validate(enabled)
}

// Resolve leaves an explicit CLI selection authoritative for this process.
func Resolve(ctx context.Context, store *state.DB, flagValue string, explicit bool) (string, error) {
	if explicit {
		return flagValue, nil
	}
	enabled, err := Load(ctx, store)
	return strings.Join(enabled, ","), err
}

func (m *Module) settings(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.store == nil {
		http.Error(w, "platform settings unavailable", http.StatusServiceUnavailable)
		return
	}
	if r.Method == http.MethodPost {
		if m.overridden {
			http.Error(w, "remove the explicit -platforms flag to configure platforms in Settings", http.StatusConflict)
			return
		}
		var req struct {
			Enabled []string `json:"enabled"`
		}
		if !httpx.ReadJSON(w, r, 1024, &req) {
			return
		}
		if err := validate(req.Enabled); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		slices.Sort(req.Enabled)
		if err := m.store.SetSetting(r.Context(), setting, strings.Join(req.Enabled, ",")); err != nil {
			httpx.ServerError(w, "saving platform settings", err)
			return
		}
		m.pending = m.reload != nil && !slices.Equal(req.Enabled, m.active)
	} else if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	enabled, err := Load(r.Context(), m.store)
	if err != nil {
		httpx.ServerError(w, "reading platform settings", err)
		return
	}
	httpx.WriteJSON(w, map[string]any{"enabled": enabled, "reloadPending": m.pending || m.recoveryPending, "overridden": m.overridden})
}
