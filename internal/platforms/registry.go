package platforms

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms/identity"
	"github.com/ocman/ocman/internal/srvtiming"
	log "github.com/sirupsen/logrus"
)

// Registry holds registered platform adapters and maintains a reverse
// lookup from session ID to owning platform for endpoints that don't
// carry the ?platform= query param.
type Registry struct {
	mu        sync.RWMutex
	order     []ID // registration order for stable Platforms() output
	byID      map[ID]Platform
	bySID     map[string]ID // session-id -> owning platform (populated by RememberSessions)
	sink      SessionEventSink
	canonical bool
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{
		byID:  make(map[ID]Platform),
		bySID: make(map[string]ID),
	}
}

// Register adds an adapter. Registering the same ID twice replaces the
// previous entry (convenient for tests; shouldn't happen in production).
func (r *Registry) Register(p Platform) {
	if p == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	id := p.ID()
	if r.canonical && !CanonicalAdapter(p) {
		log.WithField("platform", id).Error("refusing non-canonical session adapter after identity cutover")
		return
	}
	if _, exists := r.byID[id]; !exists {
		r.order = append(r.order, id)
	}
	r.byID[id] = p
	if source, ok := p.(SessionEventSource); ok {
		source.SetSessionEventSink(r.sink)
	}
}

// SessionRegistry is the routing contract shared by session services. Wrappers
// must forward CanonicalSessionIDs; dropping it silently reverts to native
// reverse-cache ownership.
type SessionRegistry interface {
	Get(ID) (Platform, bool)
	Platforms() []Platform
	PlatformForSession(context.Context, string) (Platform, bool)
	CanonicalSessionIDs() bool
}

// CanonicalAdapter reports whether an adapter exposes Ocman session IDs.
func CanonicalAdapter(p any) bool {
	canonical, ok := p.(interface{ CanonicalSessionIDs() bool })
	return ok && canonical.CanonicalSessionIDs()
}

// EnableCanonicalSessions is called once after migration and adapter wrapping,
// before serving. Later registrations and reverse lookups cannot reintroduce
// native execution keys. Pending references are not executable session IDs.
func (r *Registry) EnableCanonicalSessions() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range r.byID {
		if !CanonicalAdapter(p) {
			return fmt.Errorf("platform %s has no canonical session boundary", p.ID())
		}
	}
	r.canonical = true
	clear(r.bySID)
	return nil
}

func (r *Registry) CanonicalSessionIDs() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.canonical
}

// SetSessionEventSink wires existing adapters and any registered later.
func (r *Registry) SetSessionEventSink(sink SessionEventSink) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sink = sink
	for _, p := range r.byID {
		if source, ok := p.(SessionEventSource); ok {
			source.SetSessionEventSink(sink)
		}
	}
}

// Unregister removes an adapter by ID and drops any reverse-lookup
// cache entries pointing at it. Used by the remote manager when a remote
// is removed or disconnected so its sessions stop appearing. Registering
// the same ID again later (on reconnect) restores it. No-op for unknown
// IDs.
func (r *Registry) Unregister(id ID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.byID[id]; !ok {
		return
	}
	delete(r.byID, id)
	for i, oid := range r.order {
		if oid == id {
			r.order = append(r.order[:i], r.order[i+1:]...)
			break
		}
	}
	for sid, owner := range r.bySID {
		if owner == id {
			delete(r.bySID, sid)
		}
	}
}

// Get returns the adapter for an ID.
func (r *Registry) Get(id ID) (Platform, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.byID[id]
	return p, ok
}

// Platforms returns all registered adapters in registration order.
func (r *Registry) Platforms() []Platform {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]Platform, 0, len(r.order))
	for _, id := range r.order {
		out = append(out, r.byID[id])
	}
	return out
}

// RememberSessions records that a set of session IDs belong to the given
// platform. Call this after each Sessions() fan-out so subsequent
// per-session endpoints can resolve the owning adapter without a
// ?platform= param.
func (r *Registry) RememberSessions(platformID ID, sessions []db.Session) {
	if len(sessions) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range sessions {
		if s.ID == "" || (r.canonical && !identity.IsSessionID(s.ID)) {
			continue
		}
		r.bySID[s.ID] = platformID
	}
}

// PlatformForSession returns the adapter owning a given session ID.
//
// Resolution order:
//  1. Check the reverse-lookup cache populated by RememberSessions.
//  2. Fan out to every available adapter calling Owns. The first
//     adapter that claims the session owns it; its mapping is cached
//     for next time.
//
// Owns is required to be cheap (DB or filesystem only — no HTTP, no
// external processes). Earlier versions of this method called
// Platform.Session here, which for OpenCode triggered the full live
// fetch (lsof port discovery + 2 HTTP calls) just to identify the
// owner — adding hundreds of milliseconds to every cold-cache request.
func (r *Registry) PlatformForSession(ctx context.Context, sessionID string) (Platform, bool) {
	r.mu.RLock()
	if r.canonical && !identity.IsSessionID(sessionID) {
		r.mu.RUnlock()
		return nil, false
	}
	id, ok := r.bySID[sessionID]
	r.mu.RUnlock()

	if ok {
		if p, ok := r.Get(id); ok {
			cached := srvtiming.Begin(ctx, "resolve_cached")
			cached.EndWithDesc("sid->platform cache hit")
			return p, true
		}
	}

	// Fan-out fallback. Kept in a method so tests that only register
	// fake platforms without a populated bySID still work deterministically.
	//
	// Phase naming reflects the outcome: a successful match emits
	// `resolve_owns`, a total miss emits `resolve_miss`. We can't
	// rename a phase after Begin, so we don't decide on a name until
	// we know which case we're in — in practice that means measuring
	// the work with time.Since on a small stack-local start time.
	start := time.Now()
	for _, p := range r.Platforms() {
		if !p.Available(ctx) {
			continue
		}
		if p.Owns(ctx, sessionID) {
			r.mu.Lock()
			r.bySID[sessionID] = p.ID()
			r.mu.Unlock()
			srvtiming.Record(ctx, "resolve_owns", time.Since(start), "cold-cache Owns fan-out")
			return p, true
		}
	}
	srvtiming.Record(ctx, "resolve_miss", time.Since(start), "no adapter claimed session")
	return nil, false
}
