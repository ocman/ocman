// Package sessionupdates coalesces compact-row reads before broadcasting them.
// Producers never wait for SQL or a browser; overload falls back to invalidation.
package sessionupdates

import (
	"context"
	"sync"
	"time"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/module"
)

type Key struct{ Platform, SessionID string }
type Update struct {
	Key
	Revision uint64
	Row      *db.Session
}
type Deps struct {
	Read    func(context.Context, Key) (*db.Session, error)
	Publish func(Update)
}

type Module struct {
	d       Deps
	mu      sync.Mutex
	active  bool
	pending map[Key]uint64
	wake    chan struct{}
}

func New(d Deps) *Module {
	return &Module{d: d, pending: map[Key]uint64{}, wake: make(chan struct{}, 1)}
}
func (*Module) Name() string         { return "session-updates" }
func (*Module) Routes(module.Router) {}
func (m *Module) Start(context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.active = true
	return nil
}

// Submit bounds memory even when a source floods changes while reads stall.
// A false result asks the caller to publish the original invalidation instead.
func (m *Module) Submit(key Key, revision uint64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.active || (len(m.pending) >= 128 && m.pending[key] == 0) {
		return false
	}
	m.pending[key] = max(m.pending[key], revision)
	select {
	case m.wake <- struct{}{}:
	default:
	}
	return true
}

func (m *Module) Run(ctx context.Context) {
	defer m.Finalize()
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.wake:
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		module.RunWithRecover(m.Name(), func() { m.flush(ctx) })
	}
}

func (m *Module) Finalize() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.active = false
	clear(m.pending)
}

func (m *Module) flush(ctx context.Context) {
	m.mu.Lock()
	pending := m.pending
	m.pending = map[Key]uint64{}
	m.mu.Unlock()
	for key, revision := range pending {
		if ctx.Err() != nil {
			return
		}
		readCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
		row, err := m.d.Read(readCtx, key)
		cancel()
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			row = nil
		}
		m.d.Publish(Update{key, revision, row})
	}
}
