package hostsvc

import "sync"

// Router resolves the owning Host for a directory or an explicit remote
// owner. It is the directory analogue of Registry.PlatformForSession.
//
// ForRemote is the preferred path whenever the caller already knows the
// owner (the session list, project inventory, and machine picker all do).
// ForDir is a convenience fallback for local/legacy calls; in v1 (and
// for any path physically on the hub) it resolves to the local Host.
// Phase 8 upgrades ForDir to consult the project-inventory cache so a
// dir that unambiguously matches a remote's known project resolves to
// that remote's Host.
//
// Router is safe for concurrent use; remote hosts are registered and
// unregistered as connections come and go.
type Router struct {
	local Host

	mu      sync.RWMutex
	remotes map[string]Host // remoteID -> Host

	// dirResolver, when set, maps an absolute dir to a remoteID (or ""
	// for local). Installed by the remote Manager in Phase 8 to back
	// ForDir with the inventory cache. Nil means "everything is local".
	dirResolver func(dir string) string
}

// NewRouter creates a Router with the given local Host. local must be
// non-nil; it is the fallback owner for every unresolved directory.
func NewRouter(local Host) *Router {
	return &Router{
		local:   local,
		remotes: make(map[string]Host),
	}
}

// Local returns the in-process local Host.
func (r *Router) Local() Host { return r.local }

// RegisterRemote adds (or replaces) a remote Host keyed by its remote ID.
func (r *Router) RegisterRemote(remoteID string, h Host) {
	r.mu.Lock()
	r.remotes[remoteID] = h
	r.mu.Unlock()
}

// UnregisterRemote removes a remote Host (e.g. on disconnect/removal).
func (r *Router) UnregisterRemote(remoteID string) {
	r.mu.Lock()
	delete(r.remotes, remoteID)
	r.mu.Unlock()
}

// SetDirResolver installs the dir→remoteID resolver used by ForDir.
func (r *Router) SetDirResolver(fn func(dir string) string) {
	r.mu.Lock()
	r.dirResolver = fn
	r.mu.Unlock()
}

// Remotes returns the currently registered remote hosts, keyed by ID.
func (r *Router) Remotes() map[string]Host {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]Host, len(r.remotes))
	for id, h := range r.remotes {
		out[id] = h
	}
	return out
}

// ForRemote returns the Host for an explicit owner. An empty or "local"
// remoteID, or an unknown remote, resolves to the local Host so callers
// degrade gracefully rather than failing closed.
func (r *Router) ForRemote(remoteID string) Host {
	if remoteID == "" || remoteID == "local" {
		return r.local
	}
	r.mu.RLock()
	h, ok := r.remotes[remoteID]
	r.mu.RUnlock()
	if ok {
		return h
	}
	return r.local
}

// LookupRemote is the strict counterpart of ForRemote: it reports
// whether remoteID actually resolves to a registered owner. Callers
// holding a *persisted* remote ID (a saved schedule, a stored worktree
// record) must use this — with ForRemote a stale ID silently binds to
// the local host and the action runs on the wrong machine.
//
// An empty or "local" remoteID is the local host (ok=true).
func (r *Router) LookupRemote(remoteID string) (Host, bool) {
	if remoteID == "" || remoteID == "local" {
		return r.local, true
	}
	r.mu.RLock()
	h, ok := r.remotes[remoteID]
	r.mu.RUnlock()
	return h, ok
}

// ForDir resolves the owner of an absolute directory. It consults the
// installed dirResolver (inventory cache) when present; everything else
// resolves to the local Host.
func (r *Router) ForDir(dir string) Host {
	r.mu.RLock()
	resolver := r.dirResolver
	r.mu.RUnlock()
	if resolver == nil {
		return r.local
	}
	return r.ForRemote(resolver(dir))
}
