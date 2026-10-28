// Package remotesmod owns multi-remote management over HTTP: the hub's
// /api/remotes CRUD + reconnect, this instance's remote-access settings and
// token reveal, and the inventory refresh loop. The connection manager, its
// gRPC transport, the platform registry and the host router stay core; the
// module only drives them through Manager.
package remotesmod

import (
	"context"
	"time"

	"github.com/ocman/ocman/internal/module"
	"github.com/ocman/ocman/internal/remote"
	"github.com/ocman/ocman/internal/state"
)

// InventoryInterval is how often connected remotes' project inventories are
// refreshed so directory ownership matches fresh data (AD-8).
const InventoryInterval = 5 * time.Minute

// Manager is the hub's remote connection manager (*remote.Manager).
type Manager interface {
	List(ctx context.Context) ([]remote.RemoteStatus, error)
	Add(ctx context.Context, address, token, displayName string) (int64, error)
	Update(ctx context.Context, localID int64, displayName, address string, enabled bool, token *string) error
	Reconnect(ctx context.Context, localID int64) error
	Remove(ctx context.Context, localID int64) error
	RunInventoryLoop(ctx context.Context, interval time.Duration)
}

// Identity reads this instance's own persisted identity and token.
type Identity interface {
	InstanceIdentity(ctx context.Context) (state.InstanceIdentity, error)
}

// Access is this instance's remote-access listener as configured at startup.
type Access struct {
	InstanceID string
	ListenAddr string
	Listening  bool
	TLS        bool
}

// Deps are read at call time: the manager and access are set after New.
type Deps struct {
	// Manager returns nil on a single-host install. Return an untyped nil,
	// never a nil *remote.Manager.
	Manager func() Manager
	// Identity is nil without a state DB.
	Identity Identity
	Access   func() Access
	// Interval overrides InventoryInterval (tests).
	Interval time.Duration
}

type Module struct{ d Deps }

func NewModule(d Deps) *Module {
	if d.Interval <= 0 {
		d.Interval = InventoryInterval
	}
	return &Module{d: d}
}

func (m *Module) Name() string { return "remotes" }

// Routes checks the method (mux pattern), then auth, then localhost.
func (m *Module) Routes(r module.Router) {
	local := r.Guards().Localhost
	r.Handle("GET /api/settings/remote-access", m.handleAccess)
	r.Handle("POST /api/settings/remote-access/reveal-token", local(m.handleRevealToken))
	r.Handle("GET /api/remotes", local(m.handleList))
	r.Handle("POST /api/remotes", local(m.handleAdd))
	r.Handle("PUT /api/remotes/{id}", local(m.handleUpdate))
	r.Handle("DELETE /api/remotes/{id}", local(m.handleRemove))
	r.Handle("POST /api/remotes/{id}/reconnect", local(m.handleReconnect))
}

// Run refreshes inventories among the server's joined workers, so the loop
// never outlives shutdown. Manager's own Start/Stop stay with the caller
// that built it.
func (m *Module) Run(ctx context.Context) {
	if mgr := m.manager(); mgr != nil {
		mgr.RunInventoryLoop(ctx, m.d.Interval)
	}
}

func (m *Module) manager() Manager {
	if m.d.Manager == nil {
		return nil
	}
	return m.d.Manager()
}
