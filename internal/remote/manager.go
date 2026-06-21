package remote

import (
	"context"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

// Manager owns the hub-side remote connections: it loads saved remotes
// from state.db, dials each one, registers a remotePlatform in the
// platforms.Registry and a remoteHost in the hostsvc.Router as each
// connects, and keeps the per-remote project inventory cache (Phase 8).
//
// It is the single place that knows about RemoteConn lifecycle; the rest
// of the server sees only Platform/Host adapters via the registry/router.
type Manager struct {
	registry *platforms.Registry
	router   *hostsvc.Router
	store    *state.DB
	base     string // base platform id remotes expose (v1: "opencode")

	mu      sync.RWMutex
	remotes map[int64]*managedRemote // keyed by hub-local id
}

// managedRemote bundles a RemoteConn with its registered adapters and the
// hub-side config row it came from.
type managedRemote struct {
	localID  int64
	conn     *RemoteConn
	platform *remotePlatform
	host     *remoteHost
	name     string // hub display name (live)
}

// NewManager creates a Manager. base is the platform id remotes expose
// (v1 OpenCode-only).
func NewManager(registry *platforms.Registry, router *hostsvc.Router, store *state.DB, base string) *Manager {
	return &Manager{
		registry: registry,
		router:   router,
		store:    store,
		base:     base,
		remotes:  make(map[int64]*managedRemote),
	}
}

// Start loads saved remotes and dials the enabled ones in the background.
// Returns immediately; connections progress asynchronously so a slow or
// offline remote never blocks startup (NFR-1).
func (m *Manager) Start(ctx context.Context) {
	if m.store == nil {
		return
	}
	rows, err := m.store.ListRemotes()
	if err != nil {
		log.WithError(err).Warn("remote: loading saved remotes")
		return
	}
	for _, r := range rows {
		if !r.Enabled {
			continue
		}
		m.dial(ctx, r)
	}
}

// dial connects a single remote in the background and registers its
// adapters on success.
func (m *Manager) dial(ctx context.Context, r state.Remote) {
	token, err := m.store.RemoteToken(r.LocalID)
	if err != nil {
		log.WithError(err).WithField("remote", r.LocalID).Warn("remote: reading token")
		return
	}
	conn := NewRemoteConn(r.Address, token)
	mr := &managedRemote{localID: r.LocalID, conn: conn, name: displayName(r)}

	m.mu.Lock()
	// Replace any existing managed remote for this id.
	if old, ok := m.remotes[r.LocalID]; ok {
		m.unregisterLocked(old)
	}
	m.remotes[r.LocalID] = mr
	m.mu.Unlock()

	go m.connectAndRegister(ctx, mr, r.LocalID)
}

func (m *Manager) connectAndRegister(ctx context.Context, mr *managedRemote, localID int64) {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := mr.conn.Connect(cctx); err != nil {
		log.WithError(err).WithField("remote", localID).Warn("remote: connect failed")
		m.persistHealth(localID, mr.conn)
		return
	}
	mr.platform = newRemotePlatform(mr.conn, m.base, func() string {
		return m.displayNameFor(localID)
	})
	mr.host = newRemoteHost(mr.conn)

	m.registry.Register(mr.platform)
	m.router.RegisterRemote(mr.conn.RemoteID(), mr.host)
	m.persistHealth(localID, mr.conn)

	log.WithFields(log.Fields{
		"remote":   localID,
		"remoteId": mr.conn.RemoteID(),
		"hostname": mr.conn.Hostname(),
	}).Info("remote: connected")
}

// persistHealth writes the connection outcome back to state.db.
func (m *Manager) persistHealth(localID int64, conn *RemoteConn) {
	if m.store == nil {
		return
	}
	_ = m.store.SetRemoteHealth(
		localID,
		conn.RemoteID(),
		string(conn.Health()),
		conn.Hostname(),
		conn.ProtocolVersion(),
		conn.LastSeen().UnixMilli(),
	)
}

// displayNameFor returns the live display name for a managed remote.
func (m *Manager) displayNameFor(localID int64) string {
	m.mu.RLock()
	mr, ok := m.remotes[localID]
	m.mu.RUnlock()
	if ok && mr.name != "" {
		return mr.name
	}
	if ok && mr.conn != nil {
		return mr.conn.Hostname()
	}
	return "remote"
}

// unregisterLocked tears down a managed remote's adapters. Caller holds m.mu.
func (m *Manager) unregisterLocked(mr *managedRemote) {
	if mr.conn != nil {
		if rid := mr.conn.RemoteID(); rid != "" {
			m.router.UnregisterRemote(rid)
		}
		mr.conn.Close()
	}
}

// Stop closes every managed connection.
func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, mr := range m.remotes {
		m.unregisterLocked(mr)
	}
	m.remotes = make(map[int64]*managedRemote)
}

// Conn returns the RemoteConn for a hub-local id, if managed.
func (m *Manager) Conn(localID int64) (*RemoteConn, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	mr, ok := m.remotes[localID]
	if !ok {
		return nil, false
	}
	return mr.conn, true
}

// displayName picks the hub display name for a remote row: the explicit
// name, else the reported hostname, else the address.
func displayName(r state.Remote) string {
	if r.DisplayName != "" {
		return r.DisplayName
	}
	if r.Hostname != "" {
		return r.Hostname
	}
	return r.Address
}
