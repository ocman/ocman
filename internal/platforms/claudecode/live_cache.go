package claudecode

import (
	"sync"
	"time"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

// defaultBusyTTL is how long a "busy" state persists without a
// follow-up event before the cache reports it as "done" instead.
// Two minutes balances two failure modes:
//
//   - Too short: a slow tool invocation (e.g. a big LSP search, a
//     `make build`) would flap the dashboard to "done" mid-turn.
//   - Too long: if the Claude Code process dies mid-turn we want the
//     sidebar to stop showing "busy" within a reasonable window.
//
// Picked two minutes as a tradeoff that covers most tool invocations
// while still catching real crashes before the user reloads the page.
const defaultBusyTTL = 2 * time.Minute

// liveCache is an in-memory, goroutine-safe store of live session
// state driven by Claude Code hook events.
//
// Entries are keyed by Claude Code session UUID. The cache is
// deliberately lossy — there's no persistence across ocman restarts,
// because the data is "live status" and gets rebuilt from the next
// hook event. See D3/D5 in the Phase 5 plan.
//
// "busy" entries are subject to a TTL (defaultBusyTTL) to protect
// against the CLI dying mid-turn and leaving the UI stuck on busy.
// Terminal states ("done", "error") are authoritative and never
// expire.
type liveCache struct {
	mu      sync.RWMutex
	states  map[string]*platforms.LiveState
	busyTTL time.Duration
}

// newLiveCache creates an empty cache with the given busy-state TTL.
// Pass defaultBusyTTL in production; tests override for fast paths.
func newLiveCache(busyTTL time.Duration) *liveCache {
	return &liveCache{
		states:  make(map[string]*platforms.LiveState),
		busyTTL: busyTTL,
	}
}

// Apply mutates the cache entry for sessionID using the given delta,
// timestamped at time.Now(). See ApplyAt for the semantics.
func (c *liveCache) Apply(sessionID string, d liveStateDelta) {
	c.ApplyAt(sessionID, d, time.Now())
}

// ApplyAt is like Apply but with a caller-supplied timestamp; used by
// tests so they don't depend on wall-clock scheduling.
//
// Semantics:
//   - d.Status != "" overrides the existing status.
//   - d.PendingPermission=true sets the flag.
//   - d.ClearPendingPermission=true clears the flag, regardless of
//     d.PendingPermission. This asymmetry is deliberate — the zero
//     delta must be inert.
//   - LastEventAt is always set to the supplied timestamp when any
//     delta is applied, so staleness detection is anchored to the
//     latest signal.
//
// Applying a delta for an empty sessionID is a no-op. Ignored hook
// events should never reach ApplyAt in the first place.
func (c *liveCache) ApplyAt(sessionID string, d liveStateDelta, at time.Time) {
	if sessionID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	s, ok := c.states[sessionID]
	if !ok {
		s = &platforms.LiveState{}
		c.states[sessionID] = s
	}
	if d.Status != "" {
		s.Status = d.Status
	}
	if d.ClearPendingPermission {
		s.PendingPermission = false
	}
	if d.PendingPermission {
		s.PendingPermission = true
	}
	s.LastEventAt = at
}

// Get returns the current live state for sessionID using time.Now()
// as the reference for TTL checks. Returns nil if no hook event has
// ever been observed for this session.
func (c *liveCache) Get(sessionID string) *platforms.LiveState {
	return c.GetAt(sessionID, time.Now())
}

// GetAt is Get with a caller-supplied "now" for tests. The returned
// pointer is a defensive copy — callers may mutate it without
// affecting cache contents.
//
// Stale-busy rule: if status is "busy" and (now - LastEventAt) >
// busyTTL, we report status="done" without mutating the stored
// entry. A subsequent hook event naturally revives the live status;
// we don't need to persist the transition.
func (c *liveCache) GetAt(sessionID string, now time.Time) *platforms.LiveState {
	if sessionID == "" {
		return nil
	}
	c.mu.RLock()
	s, ok := c.states[sessionID]
	c.mu.RUnlock()
	if !ok {
		return nil
	}

	// Copy so the caller can't mutate our stored state.
	out := *s

	if out.Status == "busy" && c.busyTTL > 0 && now.Sub(out.LastEventAt) > c.busyTTL {
		out.Status = "done"
	}
	return &out
}
