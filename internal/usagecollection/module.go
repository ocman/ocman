// Package usagecollection owns opt-out product usage reporting. It never reads
// conversations, provider accounts, credentials, paths or device identifiers.
package usagecollection

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/ocman/ocman/internal/module"
	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/usagewire"
)

type Deps struct {
	Store    *state.DB
	RelayURL func() string
	Version  func() string
	Daily    func(context.Context, time.Time) (usagewire.Daily, error)
}

type Module struct {
	deps        Deps
	mu          sync.Mutex
	wake        chan struct{}
	client      *http.Client
	now         func() time.Time
	nextAttempt time.Time
	nextDaily   time.Time
	nextHealth  time.Time
	health      map[string]*healthDay
	healthEpoch uint64
	cancel      context.CancelFunc
}

func NewModule(deps Deps) *Module {
	if deps.RelayURL == nil {
		deps.RelayURL = func() string { return "" }
	}
	if deps.Version == nil {
		deps.Version = func() string { return "dev" }
	}
	return &Module{deps: deps, wake: make(chan struct{}, 1), now: time.Now,
		client: &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (m *Module) Name() string { return "usage-collection" }

// RecordActivity is called only after new foreground UI time was credited.
// It performs local bookkeeping only; the joined worker owns every network call.
func (m *Module) RecordActivity(ctx context.Context) {
	if m.deps.Store == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	enabled, err := m.enabled(ctx)
	if err != nil || !enabled {
		return
	}
	if err := m.record(ctx, m.now().UTC().Format(time.DateOnly)); err != nil {
		return
	}
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

func (m *Module) Run(ctx context.Context) {
	if m.deps.Store == nil {
		<-ctx.Done()
		return
	}
	module.RunWithRecover(m.Name(), func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		m.sendPending(ctx)
		m.sendDaily(ctx)
		m.sendHealth(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-m.wake:
				m.sendPending(ctx)
			case <-ticker.C:
				m.sendPending(ctx)
				m.sendDaily(ctx)
				m.sendHealth(ctx)
			}
		}
	})
}
