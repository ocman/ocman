package platforms

import (
	"context"
	"sync"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/srvtiming"
)

// Registry holds registered platform adapters and maintains a reverse
// lookup from session ID to owning platform for endpoints that don't
// carry the ?platform= query param.
type Registry struct {
	mu    sync.RWMutex
	order []ID // registration order for stable Platforms() output
	byID  map[ID]Platform
	bySID map[string]ID // session-id -> owning platform (populated by RememberSessions)
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
	if _, exists := r.byID[id]; !exists {
		r.order = append(r.order, id)
	}
	r.byID[id] = p
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
		if s.ID == "" {
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
	start := time.Now()
	r.mu.RLock()
	id, ok := r.bySID[sessionID]
	r.mu.RUnlock()

	if ok {
		if p, ok := r.Get(id); ok {
			srvtiming.Record(ctx, "resolve_cached", time.Since(start), "sid->platform cache hit")
			return p, true
		}
	}

	// Fan-out fallback. Kept in a method so tests that only register
	// fake platforms without a populated bySID still work deterministically.
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
