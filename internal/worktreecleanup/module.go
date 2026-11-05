// Package worktreecleanup removes safe managed checkouts seven days after
// archive and subsequent activity. Session history and branches are retained.
package worktreecleanup

import (
	"context"
	"time"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/module"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/state"
	log "github.com/sirupsen/logrus"
)

const retention = 7 * 24 * time.Hour

type Deps struct {
	Store     *state.DB
	Platforms func() []platforms.Platform
	Router    func() *hostsvc.Router
	Ready     func(string) bool
	Now       func() time.Time
	Idle      func(context.Context) error
}

type Module struct{ d Deps }

func New(d Deps) *Module             { return &Module{d: d} }
func (*Module) Name() string         { return "worktreecleanup" }
func (*Module) Routes(module.Router) {}
func (m *Module) now() time.Time {
	if m.d.Now != nil {
		return m.d.Now()
	}
	return time.Now()
}

// Run catches up after restarts and retries skipped checkouts hourly. Due
// dates are derived from persisted archive/activity timestamps, not timers.
func (m *Module) Run(ctx context.Context) {
	if m.d.Store == nil {
		return
	}
	sweep := func() {
		module.RunWithRecover("worktree-cleanup", func() {
			if err := m.Sweep(ctx); err != nil && ctx.Err() == nil {
				log.WithError(err).Warn("worktree cleanup deferred")
			}
		})
	}
	sweep()
	module.Every(ctx, "worktree-cleanup", time.Hour, func(context.Context) { sweep() })
}
