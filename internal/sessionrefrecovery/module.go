// Package sessionrefrecovery resolves disconnected-owner reference proofs and
// requests a quiescent backend reload. It never rekeys state under live workers.
package sessionrefrecovery

import (
	"context"
	"time"

	"github.com/ocman/ocman/internal/module"
)

type Deps struct {
	Scopes        func() []string
	Resolve       func(context.Context, string) (int, error)
	RequestReload func() bool
	OnError       func(string, error)
	Interval      time.Duration
	Timeout       time.Duration
}

type Module struct {
	deps        Deps
	needsReload bool
}

func New(deps Deps) *Module {
	if deps.Interval <= 0 {
		deps.Interval = 5 * time.Second
	}
	if deps.Timeout <= 0 {
		deps.Timeout = 10 * time.Second
	}
	return &Module{deps: deps}
}
func (*Module) Name() string         { return "session-reference-recovery" }
func (*Module) Routes(module.Router) {}

func (m *Module) Run(ctx context.Context) {
	tick := time.NewTicker(m.deps.Interval)
	defer tick.Stop()
	for {
		if ctx.Err() != nil || m.check(ctx) {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func (m *Module) check(ctx context.Context) bool {
	if m.needsReload {
		return m.deps.RequestReload != nil && m.deps.RequestReload()
	}
	if m.deps.Scopes == nil || m.deps.Resolve == nil {
		return false
	}
	for _, scope := range m.deps.Scopes() {
		if ctx.Err() != nil {
			return false
		}
		attempt, cancel := context.WithTimeout(ctx, m.deps.Timeout)
		bound, err := m.deps.Resolve(attempt, scope)
		cancel()
		if err != nil && m.deps.OnError != nil {
			m.deps.OnError(scope, err)
		}
		if bound > 0 {
			m.needsReload = true
		}
	}
	return m.needsReload && m.deps.RequestReload != nil && m.deps.RequestReload()
}
