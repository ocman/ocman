package remote

import (
	"context"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/db"
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

	// baseCtx is the manager's own lifetime context, replaced in Start.
	// Background connect goroutines derive from it (not a request ctx)
	// so a reconnect triggered by an HTTP handler isn't cancelled the
	// instant the handler returns.
	baseCtx     context.Context
	cancel      context.CancelFunc
	lifecycleMu sync.Mutex
	connectors  sync.WaitGroup
	stopped     bool

	mu      sync.RWMutex
	remotes map[int64]*managedRemote // keyed by hub-local id

	// inventory caches each connected remote's project list, keyed by
	// remote instance ID. Refreshed on connect and on a periodic timer
	// (AD-8). Used by ResolveTargets and the router's dir resolver.
	invMu     sync.RWMutex
	inventory map[string][]ProjectIdentity

	// beforeAdapterRegister, when non-nil, runs in the supervisor after a
	// successful connect and before the remote's adapters are published
	// and registered. Test seam for pinning the
	// disconnect-during-registration interleaving; nil in production. Set
	// before Start.
	beforeAdapterRegister func()
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
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{
		registry:  registry,
		router:    router,
		store:     store,
		base:      base,
		baseCtx:   ctx,
		cancel:    cancel,
		remotes:   make(map[int64]*managedRemote),
		inventory: make(map[string][]ProjectIdentity),
	}
	// Back the router's ForDir with the inventory cache so a dir that
	// unambiguously matches a remote's known project resolves to that
	// remote's Host (AD-16/AD-8).
	router.SetDirResolver(m.resolveDir)
	return m
}

// Start loads saved remotes and dials the enabled ones in the background.
// Returns immediately; connections progress asynchronously so a slow or
// offline remote never blocks startup (NFR-1).
func (m *Manager) Start(ctx context.Context) {
	m.lifecycleMu.Lock()
	if m.stopped {
		m.lifecycleMu.Unlock()
		return
	}
	m.cancel()
	m.baseCtx, m.cancel = context.WithCancel(ctx)
	m.lifecycleMu.Unlock()
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
		m.dial(r)
	}
}

// RunInventoryLoop periodically refreshes every connected remote's
// project inventory until ctx is cancelled (AD-8). Call once after Start.
func (m *Manager) RunInventoryLoop(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.RefreshInventories(ctx)
		}
	}
}

// dial connects a single remote in the background and registers its
// adapters on success.
func (m *Manager) dial(r state.Remote) {
	token, err := m.store.RemoteToken(r.LocalID)
	if err != nil {
		log.WithError(err).WithField("remote", r.LocalID).Warn("remote: reading token")
		return
	}
	conn := NewRemoteConn(r.Address, token)
	mr := &managedRemote{localID: r.LocalID, conn: conn, name: displayName(r)}

	m.lifecycleMu.Lock()
	if m.stopped {
		m.lifecycleMu.Unlock()
		return
	}
	m.mu.Lock()
	// Replace any existing managed remote for this id.
	if old, ok := m.remotes[r.LocalID]; ok {
		m.unregisterLocked(old)
	}
	m.remotes[r.LocalID] = mr
	m.mu.Unlock()

	m.connectors.Add(1)
	ctx := m.baseCtx
	m.lifecycleMu.Unlock()
	go func() {
		defer m.connectors.Done()
		m.connectAndRegister(ctx, mr, r.LocalID)
	}()
}

// reconnect backoff bounds. Auth/version failures are not retried (a
// restart won't fix a bad token or protocol mismatch); everything else
// (offline/dial errors) is retried with capped exponential backoff.
// reconnectBaseDelay is a var so tests can shrink it.
var (
	reconnectBaseDelay = 2 * time.Second
	reconnectMaxDelay  = 60 * time.Second
)

// connectAndRegister supervises a single remote for the manager's
// lifetime: it (re)connects with backoff, registers adapters on success,
// waits for the transport to drop, then reconnects. This handles a remote
// that is offline at startup and one that restarts later (which gets a
// fresh instance ID, so adapters are torn down and re-registered).
func (m *Manager) connectAndRegister(ctx context.Context, mr *managedRemote, localID int64) {
	delay := reconnectBaseDelay
	// loggedFailure suppresses per-retry log spam: we log the first failure
	// of a streak and stay quiet until the next successful connect resets it.
	loggedFailure := false
	for {
		if ctx.Err() != nil || !m.stillManaged(localID, mr) {
			return
		}

		cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := mr.conn.Connect(cctx)
		cancel()
		if err != nil {
			m.persistHealth(localID, mr.conn)
			// A bad token or incompatible version won't be fixed by
			// retrying; stop and leave the health reason visible.
			if h := mr.conn.Health(); h == HealthAuthFailed || h == HealthIncompatible {
				log.WithError(err).WithField("remote", localID).Warn("remote: connect failed, not retrying")
				return
			}
			entry := log.WithError(err).WithFields(log.Fields{
				"remote": localID,
				"retry":  delay,
			})
			if loggedFailure {
				entry.Debug("remote: connect failed, retrying")
			} else {
				entry.Warn("remote: connect failed, retrying")
				loggedFailure = true
			}
			if !sleepCtx(ctx, delay) {
				return
			}
			delay = nextDelay(delay)
			continue
		}
		delay = reconnectBaseDelay
		loggedFailure = false

		if m.beforeAdapterRegister != nil {
			m.beforeAdapterRegister()
		}
		// INVARIANT: nothing invoked while m.mu is held may call back
		// into the Manager. This closure does — displayNameFor takes
		// m.mu.RLock, and sync.RWMutex is not reentrant (see
		// nameForRemoteIDLocked). publishAdapters below hands the
		// platform to registry.Register while holding m.mu; that is
		// safe only because Register *stores* the adapter and never
		// calls it. Anything added there (or in router.RegisterRemote)
		// that reads a platform's display name self-deadlocks while
		// holding m.mu, stalling every remote operation.
		platform := newRemotePlatform(mr.conn, m.base, func() string {
			return m.displayNameFor(localID)
		})
		host := newRemoteHost(mr.conn)
		if !m.publishAdapters(localID, mr, platform, host) {
			// Superseded while connecting, or an unusable handshake: a
			// newer dial or a disconnect/removal owns this id now. Record
			// the connect we did make, drop the transport we just brought
			// up, and stop supervising.
			m.persistHealth(localID, mr.conn)
			mr.conn.Close()
			return
		}
		m.persistHealth(localID, mr.conn)
		m.refreshInventory(ctx, mr)

		log.WithFields(log.Fields{
			"remote":   localID,
			"remoteId": mr.conn.RemoteID(),
			"hostname": mr.conn.Hostname(),
		}).Info("remote: connected")

		// Block until the transport drops (remote restart / network
		// loss), then tear down the now-stale adapters and reconnect.
		mr.conn.WaitForDrop(ctx)
		if ctx.Err() != nil || !m.stillManaged(localID, mr) {
			return
		}
		m.unregisterAdapters(mr)
		mr.conn.markOffline()
		m.persistHealth(localID, mr.conn)
		log.WithField("remote", localID).Info("remote: disconnected, reconnecting")
	}
}

// publishAdapters publishes mr's adapters and registers them with the
// registry/router, but only while mr is still the managed remote for
// localID and the handshake yielded a usable remote ID. Returns false
// otherwise, in which case nothing was registered and the caller must
// stop supervising mr.
//
// Ownership is checked and the registration performed in one critical
// section, because the two must be atomic with respect to disconnect():
// with the check, the publication and the registration as separate steps,
// a disconnect landing in between removed the manager entry first (finding
// nothing registered yet, so unregisterLocked was a no-op) and this
// goroutine then registered adapters that no manager entry was left to
// remove — a stale platform/host wired to a dead connection forever.
// Serialising on m.mu means a concurrent disconnect either runs first (we
// see we're superseded and register nothing) or runs after (it tears down
// exactly what we published).
//
// LOCK ORDER: m.mu is held across registry.Register / router.
// RegisterRemote. That order is not introduced here — unregisterLocked
// has always run registry.Unregister, router.UnregisterRemote and
// conn.Close under m.mu, on all three of its callers (dial, disconnect,
// Stop). Hoisting only this call-out behind a separate outer mutex would
// therefore leave the coupling in place while adding a lock, so the
// single critical section stays. What holds it up is one invariant, and
// the code depends on it rather than on the reviewer noticing:
//
//	INVARIANT: nothing reached from inside m.mu may call back into the
//	Manager. registry.Register and router.RegisterRemote only store the
//	adapter; the platform's display-name closure (see connectAndRegister)
//	*does* re-enter (m.mu.RLock), so a registry/router hook that ever
//	calls it — or any capability probe on the host — self-deadlocks here,
//	holding m.mu against every other remote operation.
//
// Breaking that invariant means moving both this and unregisterLocked's
// call-outs out from under m.mu behind one outermost registration mutex,
// not patching this function alone.
func (m *Manager) publishAdapters(localID int64, mr *managedRemote, platform *remotePlatform, host *remoteHost) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.remotes[localID] != mr {
		return false
	}
	// No remote ID means the handshake gave us nothing to address the
	// remote by: the compound platform id would be "r-:opencode" and
	// there is no router key at all. Register neither half rather than
	// half of it — an unaddressable platform is a leak, not a degraded
	// mode.
	rid := mr.conn.RemoteID()
	if rid == "" {
		log.WithField("remote", localID).Warn("remote: connected without an instance ID; not registering adapters")
		return false
	}
	for otherLocalID, other := range m.remotes {
		if otherLocalID == localID || other.platform == nil || other.conn == nil {
			continue
		}
		if other.conn.RemoteID() == rid {
			log.WithFields(log.Fields{
				"remote":           localID,
				"conflictingRemote": otherLocalID,
				"remoteId":         rid,
			}).Warn("remote: duplicate instance ID; not registering adapters")
			return false
		}
	}
	// mr.platform / mr.host are read under m.mu by sessionCount and
	// unregisterLocked, so publish them under the same lock — a
	// concurrent Stop()/disconnect() must never observe a half-written
	// adapter pair.
	mr.platform, mr.host = platform, host
	m.registry.Register(platform)
	m.router.RegisterRemote(rid, host)
	return true
}

// stillManaged reports whether mr is the current managed remote for
// localID. A superseding dial (Reconnect/Update) or a disconnect replaces
// or removes it, which must stop this supervisor.
func (m *Manager) stillManaged(localID int64, mr *managedRemote) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.remotes[localID] == mr
}

// unregisterAdapters removes a managed remote's registry/router entries
// (without closing the conn, which the supervisor keeps to reconnect).
// Clears the fields under m.mu for the same reason the supervisor
// publishes them under it, then does the deregistration outside.
func (m *Manager) unregisterAdapters(mr *managedRemote) {
	m.mu.Lock()
	platform, host := mr.platform, mr.host
	mr.platform, mr.host = nil, nil
	m.mu.Unlock()

	if platform != nil {
		m.registry.Unregister(platform.ID())
	}
	if host != nil && mr.conn != nil {
		if rid := mr.conn.RemoteID(); rid != "" {
			m.router.UnregisterRemote(rid)
			m.evictInventory(rid)
		}
	}
}

// evictInventory drops a remote's cached project list. Must happen
// wherever the router entry is dropped: resolveDir would otherwise keep
// mapping the remote's dirs to a dead remote ID, which ForDir then
// degrades to the local host — silently running the action on the hub.
func (m *Manager) evictInventory(remoteID string) {
	m.invMu.Lock()
	delete(m.inventory, remoteID)
	m.invMu.Unlock()
}

// sleepCtx sleeps for d unless ctx is cancelled first. Returns false if
// cancelled.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// nextDelay doubles d up to reconnectMaxDelay.
func nextDelay(d time.Duration) time.Duration {
	d *= 2
	if d > reconnectMaxDelay {
		return reconnectMaxDelay
	}
	return d
}

// refreshInventory fetches a connected remote's project inventory and
// stores it in the cache keyed by the remote's instance ID (AD-8).
func (m *Manager) refreshInventory(ctx context.Context, mr *managedRemote) {
	if mr.host == nil || mr.conn.RemoteID() == "" {
		return
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	idents, err := mr.host.ProjectIdentities(cctx)
	if err != nil {
		return
	}
	m.invMu.Lock()
	m.inventory[mr.conn.RemoteID()] = idents
	m.invMu.Unlock()
}

// RefreshInventories re-fetches every connected remote's inventory. Call
// periodically and on reconnect (AD-8). Safe to call concurrently.
func (m *Manager) RefreshInventories(ctx context.Context) {
	m.mu.RLock()
	managed := make([]*managedRemote, 0, len(m.remotes))
	for _, mr := range m.remotes {
		managed = append(managed, mr)
	}
	m.mu.RUnlock()
	for _, mr := range managed {
		m.refreshInventory(ctx, mr)
	}
}

// resolveDir maps an absolute directory to the owning remote ID, or ""
// for local. A dir that exactly matches a remote's known project path
// resolves to that remote (AD-16b prefers explicit owner refs; this is
// the ForDir inference fallback).
func (m *Manager) resolveDir(dir string) string {
	if dir == "" {
		return ""
	}
	m.invMu.RLock()
	defer m.invMu.RUnlock()
	for remoteID, idents := range m.inventory {
		for _, p := range idents {
			if p.Dir == dir {
				return remoteID
			}
		}
	}
	return ""
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
	var name string
	var conn *RemoteConn
	if ok {
		// mr.name is written by updateName under m.mu; read it here.
		name, conn = mr.name, mr.conn
	}
	m.mu.RUnlock()
	if name != "" {
		return name
	}
	if conn != nil {
		return conn.Hostname()
	}
	return "remote"
}

// unregisterLocked tears down a managed remote's adapters. Caller holds m.mu.
func (m *Manager) unregisterLocked(mr *managedRemote) {
	if mr.platform != nil {
		m.registry.Unregister(mr.platform.ID())
	}
	if mr.conn != nil {
		if rid := mr.conn.RemoteID(); rid != "" {
			m.router.UnregisterRemote(rid)
			m.evictInventory(rid)
		}
		mr.conn.Close()
	}
}

// Stop waits for every connector to finish before tearing down its adapters.
func (m *Manager) Stop() {
	m.lifecycleMu.Lock()
	if !m.stopped {
		m.stopped = true
		m.cancel()
	}
	m.lifecycleMu.Unlock()
	m.connectors.Wait()

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

// RemoteStatus is the hub-facing view of one configured remote, merging
// the persisted config with the live connection state. Tokens are never
// included.
type RemoteStatus struct {
	LocalID         int64  `json:"localId"`
	RemoteID        string `json:"remoteId,omitempty"`
	DisplayName     string `json:"displayName"`
	Address         string `json:"address"`
	Enabled         bool   `json:"enabled"`
	Health          string `json:"health"`
	Hostname        string `json:"hostname"`
	ProtocolVersion int    `json:"protocolVersion"`
	LastSeen        int64  `json:"lastSeen"`
	SessionCount    int    `json:"sessionCount"`
}

// List returns the status of every configured remote, merging persisted
// rows with live health from the active connections.
func (m *Manager) List() ([]RemoteStatus, error) {
	if m.store == nil {
		return nil, nil
	}
	rows, err := m.store.ListRemotes()
	if err != nil {
		return nil, err
	}
	out := make([]RemoteStatus, 0, len(rows))
	for _, r := range rows {
		st := RemoteStatus{
			LocalID:         r.LocalID,
			RemoteID:        r.RemoteID,
			DisplayName:     r.DisplayName,
			Address:         r.Address,
			Enabled:         r.Enabled,
			Health:          r.LastHealth,
			Hostname:        r.Hostname,
			ProtocolVersion: r.ProtocolVersion,
			LastSeen:        r.LastSeen,
		}
		// Overlay live state when connected.
		if conn, ok := m.Conn(r.LocalID); ok {
			st.Health = string(conn.Health())
			if conn.RemoteID() != "" {
				st.RemoteID = conn.RemoteID()
			}
			if conn.Hostname() != "" {
				st.Hostname = conn.Hostname()
			}
			st.SessionCount = m.sessionCount(r.LocalID)
		}
		out = append(out, st)
	}
	return out, nil
}

// sessionCount returns the number of sessions currently known for a
// managed remote (from the platform adapter's ownership cache).
func (m *Manager) sessionCount(localID int64) int {
	m.mu.RLock()
	mr, ok := m.remotes[localID]
	var platform *remotePlatform
	if ok {
		platform = mr.platform
	}
	m.mu.RUnlock()
	if platform == nil {
		return 0
	}
	platform.mu.RLock()
	defer platform.mu.RUnlock()
	return len(platform.owned)
}

// Add persists a new remote and dials it in the background. Returns the
// new hub-local id.
func (m *Manager) Add(address, token, displayName string) (int64, error) {
	id, err := m.store.AddRemote(address, token, displayName)
	if err != nil {
		return 0, err
	}
	r, err := m.store.GetRemote(id)
	if err != nil {
		return id, err
	}
	m.dial(r)
	return id, nil
}

// Update edits a remote's config and reconnects with the new settings.
func (m *Manager) Update(localID int64, displayName, address string, enabled bool, token *string) error {
	if err := m.store.UpdateRemoteConfig(localID, displayName, address, enabled, token); err != nil {
		return err
	}
	m.updateName(localID, displayName)
	return m.Reconnect(localID)
}

// updateName refreshes the live display name on a managed remote.
func (m *Manager) updateName(localID int64, name string) {
	m.mu.Lock()
	if mr, ok := m.remotes[localID]; ok {
		mr.name = name
	}
	m.mu.Unlock()
}

// Reconnect tears down and re-dials a remote (or disconnects it when
// disabled).
func (m *Manager) Reconnect(localID int64) error {
	r, err := m.store.GetRemote(localID)
	if err != nil {
		return err
	}
	m.disconnect(localID)
	if !r.Enabled {
		return nil
	}
	m.dial(r)
	return nil
}

// Remove disconnects and deletes a remote.
func (m *Manager) Remove(localID int64) error {
	m.disconnect(localID)
	return m.store.DeleteRemote(localID)
}

// disconnect tears down a managed remote's connection and adapters.
func (m *Manager) disconnect(localID int64) {
	m.mu.Lock()
	if mr, ok := m.remotes[localID]; ok {
		m.unregisterLocked(mr)
		delete(m.remotes, localID)
	}
	m.mu.Unlock()
}

// TargetCandidate is one machine that has a given project checked out,
// returned by ResolveTargets for the new-session machine picker (AD-15).
type TargetCandidate struct {
	RemoteID   string `json:"remoteId"`
	RemoteName string `json:"remoteName"`
	Platform   string `json:"platform"`
	Dir        string `json:"dir"`
}

// ResolveTargets computes the project identity for dir (using the given
// origin) and returns the machines whose inventory contains a matching
// project (AD-15). The local machine is included via localProjects, which
// the caller supplies from its own host inventory. The result drives the
// frontend chooser: 1 candidate -> auto-select, >1 -> prompt, 0 -> pick a
// remote.
func (m *Manager) ResolveTargets(dir, origin string, localProjects []ProjectIdentity) []TargetCandidate {
	key := NormalizeProjectIdentity(origin, dir)
	var out []TargetCandidate

	// Local machine.
	for _, p := range localProjects {
		if p.Key == key {
			out = append(out, TargetCandidate{
				RemoteID:   "local",
				RemoteName: "This machine",
				Platform:   m.base,
				Dir:        p.Dir,
			})
			break
		}
	}

	// Remotes.
	m.invMu.RLock()
	inv := make(map[string][]ProjectIdentity, len(m.inventory))
	for id, idents := range m.inventory {
		inv[id] = idents
	}
	m.invMu.RUnlock()

	for remoteID, idents := range inv {
		for _, p := range idents {
			if p.Key == key {
				out = append(out, TargetCandidate{
					RemoteID:   remoteID,
					RemoteName: m.nameForRemoteID(remoteID),
					Platform:   CompoundPlatformID(remoteID, m.base),
					Dir:        p.Dir,
				})
				break
			}
		}
	}
	return out
}

// EnabledRemotes returns the connected remotes as picker candidates,
// regardless of whether they have the project — used for the zero-match
// "pick a machine" path (AD-15).
func (m *Manager) EnabledRemotes() []TargetCandidate {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]TargetCandidate, 0, len(m.remotes))
	for _, mr := range m.remotes {
		if mr.conn == nil || mr.conn.RemoteID() == "" || mr.conn.Health() != HealthConnected {
			continue
		}
		rid := mr.conn.RemoteID()
		out = append(out, TargetCandidate{
			RemoteID:   rid,
			RemoteName: m.nameForRemoteIDLocked(rid),
			Platform:   CompoundPlatformID(rid, m.base),
		})
	}
	return out
}

// RemoteProjects returns every connected remote's cached project
// inventory as ProjectStats, tagged with the owning remote's ID, name
// and compound platform id. Aggregate stats (session/token counts) are
// carried over from the remote's inventory records.
func (m *Manager) RemoteProjects() []db.ProjectStats {
	m.invMu.RLock()
	inv := make(map[string][]ProjectIdentity, len(m.inventory))
	for id, idents := range m.inventory {
		inv[id] = idents
	}
	m.invMu.RUnlock()

	var out []db.ProjectStats
	for remoteID, idents := range inv {
		name := m.nameForRemoteID(remoteID)
		platform := CompoundPlatformID(remoteID, m.base)
		for _, p := range idents {
			out = append(out, db.ProjectStats{
				Directory:      p.Dir,
				SessionCount:   p.SessionCount,
				MessageCount:   p.MessageCount,
				LastUsed:       p.LastUsed,
				TotalTokensIn:  p.TotalTokensIn,
				TotalTokensOut: p.TotalTokensOut,
				TotalCost:      p.TotalCost,
				RemoteID:       remoteID,
				RemoteName:     name,
				Platform:       platform,
			})
		}
	}
	return out
}

// nameForRemoteID returns the display name for a connected remote.
func (m *Manager) nameForRemoteID(remoteID string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.nameForRemoteIDLocked(remoteID)
}

// nameForRemoteIDLocked is nameForRemoteID for callers already holding
// m.mu. Go's RWMutex is not reentrant: a writer arriving between an
// outer RLock and a nested one blocks both, deadlocking the manager.
func (m *Manager) nameForRemoteIDLocked(remoteID string) string {
	for _, mr := range m.remotes {
		if mr.conn != nil && mr.conn.RemoteID() == remoteID {
			if mr.name != "" {
				return mr.name
			}
			return mr.conn.Hostname()
		}
	}
	return remoteID
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
