package webhook

import (
	"context"
	"net/http"
	"runtime/debug"
	"sync"
	"time"

	mcpserver "github.com/mark3labs/mcp-go/server"
	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/module"
	"github.com/NoUseFreak/ocman/internal/state"
)

// historyCleanupInterval spaces webhook history sweeps; retention is 30 days,
// so hourly keeps up without scanning on every inbox poll.
const historyCleanupInterval = time.Hour

// Routines is what webhooks need from the routines module: a dispatcher, and
// a lookup to check a subscription targets a routine on this machine.
type Routines interface {
	RoutineDispatcher
	Get(ctx context.Context, id string) (state.Routine, error)
}

type Deps struct {
	Store    *state.DB
	Routines Routines
	// RelayURL is the share relay, the fallback when Settings names none.
	RelayURL func() string
}

// Module mounts webhook inboxes: their REST routes and relay setting, one
// poller per inbox, delivery-history expiry, and the webhooks MCP tool.
type Module struct {
	store    *state.DB
	routines Routines
	relayURL func() string

	// keyMu serializes relay-side inbox mutations (key resets, secret
	// changes) with their local record.
	// ponytail: one lock for every inbox's key and secret changes; per-inbox locks if that contends.
	keyMu sync.Mutex

	// mu guards ctx, initial and pollers.Go: Run waits for pollers only
	// once no request can start another.
	mu      sync.Mutex
	ctx     context.Context
	pollers sync.WaitGroup
	initial []state.WebhookInbox
}

var (
	_ module.Module       = (*Module)(nil)
	_ module.Starter      = (*Module)(nil)
	_ module.Runner       = (*Module)(nil)
	_ module.ToolProvider = (*Module)(nil)
)

func NewModule(deps Deps) *Module {
	if deps.RelayURL == nil {
		deps.RelayURL = func() string { return "" }
	}
	return &Module{store: deps.Store, routines: deps.Routines, relayURL: deps.RelayURL}
}

func (m *Module) Name() string { return "webhooks" }

func (m *Module) Routes(r module.Router) {
	r.Local("/api/webhook-inboxes", m.handleInboxes)
	r.Local("/api/webhook-inboxes/", m.handleInboxes)
	r.LocalSetting("/api/settings/webhook-relay", m.handleRelaySetting)
}

// Start loads the inboxes so a broken store fails startup, not a poller.
func (m *Module) Start(ctx context.Context) error {
	inboxes, err := m.store.ListWebhookInboxes(context.WithoutCancel(ctx))
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.initial = append(m.initial, inboxes...)
	m.mu.Unlock()
	return nil
}

// Run polls every inbox and expires delivery history until ctx ends, then
// waits for every poller, including ones started by later requests.
func (m *Module) Run(ctx context.Context) {
	m.mu.Lock()
	m.ctx = ctx
	initial := m.initial
	m.initial = nil
	m.mu.Unlock()
	for _, inbox := range initial {
		m.startPoller(inbox)
	}
	ticker := time.NewTicker(historyCleanupInterval)
	defer ticker.Stop()
	for {
		m.cleanHistory(ctx)
		select {
		case <-ctx.Done():
			m.mu.Lock() // a startPoller after this sees ctx done
			m.mu.Unlock()
			m.pollers.Wait()
			return
		case <-ticker.C:
		}
	}
}

// startPoller retains inboxes created before Run installs its context.
// After shutdown it is a no-op; the next startup polls the inbox.
func (m *Module) startPoller(inbox state.WebhookInbox) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ctx == nil {
		m.initial = append(m.initial, inbox)
		return
	}
	if m.ctx.Err() != nil {
		return
	}
	ctx := m.ctx
	m.pollers.Go(func() { (&Poller{Store: m.store, Inbox: inbox, Routines: m.routines}).Run(ctx) })
}

// cleanHistory expires delivery rows and files independent of whether any
// inbox still exists: a revoked last inbox must not leave files behind.
func (m *Module) cleanHistory(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			log.WithFields(log.Fields{"panic": r, "stack": string(debug.Stack())}).Error("webhook history cleanup panicked, continuing on next tick")
		}
	}()
	if err := m.store.CleanupWebhookHistory(ctx, time.Now().Add(-state.WebhookHistoryRetention).UnixMilli()); err != nil {
		log.WithError(err).Warn("webhook history cleanup")
	}
}

func (m *Module) Tools() []mcpserver.ServerTool {
	return webhookServerTools(http.HandlerFunc(m.handleInboxes))
}
