package opencode

import (
	"context"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/telemetry"
)

// Cache-instrumentation handles for the bare-map caches in this file.
// httpCache instances carry their own handle inside the struct;
// here we mirror the pattern at package level because each cache has
// its own (cache-specific) data layout. Names match the convention
// used elsewhere ("opencode.<purpose>") so all ocman caches sit
// under one filterable label in Grafana/Mimir.
var (
	recentModelsMetrics    = telemetry.CacheMetrics{Name: "opencode.recent_models"}
	sessionDefaultsMetrics = telemetry.CacheMetrics{Name: "opencode.session_defaults"}
	sessionsListMetrics    = telemetry.CacheMetrics{Name: "opencode.sessions_list"}
)

func init() {
	// Size gauges for the bare-map caches. Each closure takes its
	// matching mutex under the read lock so a concurrent write to
	// the cache during metric collection can't trigger a map-read
	// race. The closures stay valid for the lifetime of the
	// process; tests never re-register because the var bindings
	// above are package-level.
	telemetry.RegisterCacheSizeGauge("opencode.recent_models", func() int64 {
		recentModelsMu.RLock()
		defer recentModelsMu.RUnlock()
		// recentModelsCached is a single-entry struct (not a map).
		// Report 1 when a value is cached, 0 when expired or unset.
		if recentModelsCached.expiresAt.IsZero() || time.Now().After(recentModelsCached.expiresAt) {
			return 0
		}
		return 1
	})
	telemetry.RegisterCacheSizeGauge("opencode.session_defaults", func() int64 {
		sessionDefaultsMu.RLock()
		defer sessionDefaultsMu.RUnlock()
		return int64(len(sessionDefaultsCached))
	})
	telemetry.RegisterCacheSizeGauge("opencode.sessions_list", func() int64 {
		sessionsMu.RLock()
		defer sessionsMu.RUnlock()
		return int64(len(sessionsCached))
	})
}

// SessionModels feeds the model picker with three slowly-changing
// global ingredients (recently-used model pairs from the OpenCode DB,
// the user's per-platform favorites from state.db, and the upstream
// /provider catalog) plus one per-session ingredient (the session's
// default model).
//
// recentModelsCache memoises the recents read with a short TTL +
// singleflight so concurrent /api/session/{id}/models requests
// collapse into one DB hit. The query joins session × message and
// groups across the 50 most recently updated sessions; on a busy DB
// it's by far the most expensive ingredient (favorites is a small
// state.db read, /provider is already cached upstream by
// catalogCache, session-default is one row by primary key).
//
// 30s of staleness is invisible to the user — recent models change
// at human pace (after a few prompts), and the picker shows the live
// /provider catalog separately so a brand-new model is still
// reachable before it lands in recents. Failures are not cached: a
// transient DB error should retry on the next request, not be
// remembered for 30s.

const recentModelsTTL = 30 * time.Second

type recentModelsEntry struct {
	models    []db.RecentModel
	expiresAt time.Time
}

var (
	recentModelsMu     sync.RWMutex
	recentModelsCached recentModelsEntry
	recentModelsFlight singleflight.Group
)

// dbRecentModels is the subset of *db.DB used by getRecentModelsCached.
// Defined as an interface so tests can stub the read without spinning
// up a SQLite database.
type dbRecentModels interface {
	GetRecentModels(sessionLimit, maxResults int) ([]db.RecentModel, error)
}

// getRecentModelsCached returns a (possibly cached) result of
// d.GetRecentModels(50, 10). Concurrent callers on a cold cache are
// coalesced via singleflight so only one DB scan runs.
func getRecentModelsCached(d dbRecentModels) ([]db.RecentModel, error) {
	ctx := context.Background()
	recentModelsMu.RLock()
	if !time.Now().After(recentModelsCached.expiresAt) {
		out := recentModelsCached.models
		recentModelsMu.RUnlock()
		recentModelsMetrics.RecordHit(ctx)
		return out, nil
	}
	recentModelsMu.RUnlock()
	recentModelsMetrics.RecordMiss(ctx)

	v, err, _ := recentModelsFlight.Do("recents", func() (interface{}, error) {
		// Re-check inside the flight slot; another caller may have
		// just refilled the cache while we were queuing. Not
		// counted as a hit — see the corresponding comment in
		// httpCache.getOrFetch for why.
		recentModelsMu.RLock()
		if !time.Now().After(recentModelsCached.expiresAt) {
			out := recentModelsCached.models
			recentModelsMu.RUnlock()
			return out, nil
		}
		recentModelsMu.RUnlock()

		models, err := d.GetRecentModels(50, 10)
		if err != nil {
			return nil, err
		}
		recentModelsMu.Lock()
		recentModelsCached = recentModelsEntry{
			models:    models,
			expiresAt: time.Now().Add(recentModelsTTL),
		}
		recentModelsMu.Unlock()
		return models, nil
	})
	if err != nil {
		return nil, err
	}
	models, _ := v.([]db.RecentModel)
	return models, nil
}

// GetSessionDefaults is by far the most expensive read in the
// SessionModels / Session / SessionInfo paths: the underlying SQL
// joins every message in the database against the session table and
// applies six json_extract calls per row in the WHERE clause. With
// hundreds of sessions and tens of thousands of messages it routinely
// burns 500–1400ms per call. It's also called from THREE places per
// session-detail mount (live Session fetch + DB fallback + the
// /models handler), so the same expensive query runs back-to-back
// even though the answer is identical across calls.
//
// sessionDefaultsCache absorbs all that. The query result is
// deterministic in (excludeSessionID, directory) so the cache key
// pairs both. TTL is 30s — same scale as the recents cache, since
// "most recent agent/model used in this directory" only changes
// after the user starts a fresh session and gets a few turns in.
// Failures are not cached.

const sessionDefaultsTTL = 30 * time.Second

type sessionDefaultsKey struct {
	excludeSessionID string
	directory        string
}

type sessionDefaultsEntry struct {
	defaults  db.SessionDefaults
	expiresAt time.Time
}

var (
	sessionDefaultsMu     sync.RWMutex
	sessionDefaultsCached = map[sessionDefaultsKey]sessionDefaultsEntry{}
	sessionDefaultsFlight singleflight.Group
)

// dbSessionDefaults is the subset of *db.DB used by
// getSessionDefaultsCached. Defined as an interface so tests can
// stub the read.
type dbSessionDefaults interface {
	GetSessionDefaults(sessionID, directory string) (db.SessionDefaults, error)
}

// getSessionDefaultsCached returns a (possibly cached) result of
// d.GetSessionDefaults. Cache misses on the same key are coalesced
// via singleflight so a SessionDetail mount fan-out runs the
// expensive join exactly once.
func getSessionDefaultsCached(d dbSessionDefaults, excludeSessionID, directory string) (db.SessionDefaults, error) {
	ctx := context.Background()
	key := sessionDefaultsKey{excludeSessionID: excludeSessionID, directory: directory}

	sessionDefaultsMu.RLock()
	if e, ok := sessionDefaultsCached[key]; ok && !time.Now().After(e.expiresAt) {
		sessionDefaultsMu.RUnlock()
		sessionDefaultsMetrics.RecordHit(ctx)
		return e.defaults, nil
	}
	sessionDefaultsMu.RUnlock()
	sessionDefaultsMetrics.RecordMiss(ctx)

	flightKey := excludeSessionID + "|" + directory
	v, err, _ := sessionDefaultsFlight.Do(flightKey, func() (interface{}, error) {
		// Re-check inside the flight slot. Not counted as a hit;
		// see httpCache.getOrFetch for the rationale.
		sessionDefaultsMu.RLock()
		if e, ok := sessionDefaultsCached[key]; ok && !time.Now().After(e.expiresAt) {
			sessionDefaultsMu.RUnlock()
			return e.defaults, nil
		}
		sessionDefaultsMu.RUnlock()

		defaults, err := d.GetSessionDefaults(excludeSessionID, directory)
		if err != nil {
			return db.SessionDefaults{}, err
		}
		sessionDefaultsMu.Lock()
		sessionDefaultsCached[key] = sessionDefaultsEntry{
			defaults:  defaults,
			expiresAt: time.Now().Add(sessionDefaultsTTL),
		}
		sessionDefaultsMu.Unlock()
		return defaults, nil
	})
	if err != nil {
		return db.SessionDefaults{}, err
	}
	defaults, _ := v.(db.SessionDefaults)
	return defaults, nil
}

// db.GetSessions runs seven correlated json_extract subqueries
// against a multi-thousand-row session table joined with a
// multi-tens-of-thousands-row message table. On a representative
// developer DB it costs 250–290 ms per call. The dashboard polls
// /api/sessions and /api/sessions/notify (which both call
// GetSessions) every few seconds, so without caching we pay the
// full cost on every poll.
//
// 3s TTL is the trade-off: short enough that "I started a new
// session in another tab" feels instant, long enough that the
// 5s poll cycle hits the cache after the first miss. The cache
// is keyed by directory only — directory-filtered listings (the
// project drill-down view) and the global listing are
// independent, but the `since` filter is applied to the cached
// (unfiltered) slice in Go so callers passing a rolling
// `Date.now() - LOOKBACK` value don't blow up the keyspace
// (each poll would otherwise be a fresh key, leaking ~one map
// entry per poll forever — see the heap-growth investigation
// in spec/perf-notes). The post-filter is cheap: a single
// integer compare per row, applied to a slice that's already
// in cache.
//
// Subsequent stats overlay (live connection, pending prompts)
// still runs uncached because it depends on transient OpenCode
// HTTP state that's already cached at finer granularity
// (pendingPromptCache, port discovery).

// sessionsTTL must exceed the frontend poll interval (5s for the
// dashboard/project views, ~10s for notify) or every poll lands on
// an expired entry and pays the full ~5s GetSessions query. A
// background refresher (StartSessionsRefresher) re-runs the
// unfiltered query every sessionsRefreshInterval, so this TTL only
// needs to outlive the gap between refreshes plus slack.
const sessionsTTL = 15 * time.Second

// sessionsRefreshInterval is how often the background refresher warms
// the unfiltered ("") sessions cache. Kept below sessionsTTL so the
// entry never goes cold between refreshes; reads are then always a
// cache hit and never block on the query.
const sessionsRefreshInterval = 4 * time.Second

type sessionsKey struct {
	directory string
}

type sessionsEntry struct {
	sessions  []db.Session
	expiresAt time.Time
}

var (
	sessionsMu     sync.RWMutex
	sessionsCached = map[sessionsKey]sessionsEntry{}
	sessionsFlight singleflight.Group
)

// StartSessionsRefresher keeps the unfiltered sessions cache warm by
// re-running GetSessions("", 0) every sessionsRefreshInterval until
// ctx is cancelled. With a warm cache, /api/sessions and notify polls
// read from memory instead of blocking ~5s on the query. The refresh
// goes through getSessionsCached so it shares the singleflight slot
// and writes the same cache entry that request handlers read.
func StartSessionsRefresher(ctx context.Context, d dbGetSessions) {
	go func() {
		// Warm immediately so the first request after startup hits cache.
		_, _ = getSessionsCached(d, "", 0)
		t := time.NewTicker(sessionsRefreshInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				// Force a fresh query by expiring the entry, then refetch.
				sessionsMu.Lock()
				delete(sessionsCached, sessionsKey{directory: ""})
				sessionsMu.Unlock()
				_, _ = getSessionsCached(d, "", 0)
			}
		}
	}()
}

// ResetCachesForTests clears every package-level cache (recents,
// session defaults, sessions list). Call this from integration tests
// that seed a fresh DB so a previous test's cache doesn't leak in.
//
// Exported (capitalised) so the server-package integration tests can
// reach it without a circular import. Production code never needs it
// — the TTLs handle eventual freshness on their own.
func ResetCachesForTests() {
	recentModelsMu.Lock()
	recentModelsCached = recentModelsEntry{}
	recentModelsMu.Unlock()

	sessionDefaultsMu.Lock()
	sessionDefaultsCached = map[sessionDefaultsKey]sessionDefaultsEntry{}
	sessionDefaultsMu.Unlock()

	sessionsMu.Lock()
	sessionsCached = map[sessionsKey]sessionsEntry{}
	sessionsMu.Unlock()
}

// dbGetSessions is the subset of *db.DB used by getSessionsCached.
// Defined as an interface so tests can stub the read.
type dbGetSessions interface {
	GetSessions(directory string, since int64) ([]db.Session, error)
}

// getSessionsCached returns a (possibly cached) result of
// d.GetSessions(directory, 0), then applies the `since` filter
// in Go. Concurrent callers on the same directory share a single
// underlying query via singleflight.
//
// `since` is intentionally NOT part of the cache key. The
// frontend's notify poller passes Date.now() - LOOKBACK on every
// tick, so a since-keyed cache would never hit and would leak
// one map entry per poll. By caching the unfiltered slice and
// filtering on read, every poll within the TTL window hits cache
// and the keyspace stays bounded by the number of distinct
// directories (a small finite set: "" + per-project-detail).
//
// The DB query for since=0 returns a small superset of any
// since>0 query; cost is dominated by the join, not the row
// count, so this is effectively free vs. the previous behaviour.
//
// The returned slice is shared with other cache readers; callers
// must NOT mutate it. The OpenCode adapter only ever appends
// per-session metadata to a copy of each Session struct, never
// to the slice itself, so this is safe in practice. If a future
// caller needs to mutate the result, copy it first.
func getSessionsCached(d dbGetSessions, directory string, since int64) ([]db.Session, error) {
	ctx := context.Background()
	key := sessionsKey{directory: directory}

	sessionsMu.RLock()
	if e, ok := sessionsCached[key]; ok && !time.Now().After(e.expiresAt) {
		sessionsMu.RUnlock()
		sessionsListMetrics.RecordHit(ctx)
		return filterSessionsBySince(e.sessions, since), nil
	}
	sessionsMu.RUnlock()
	sessionsListMetrics.RecordMiss(ctx)

	v, err, _ := sessionsFlight.Do(directory, func() (interface{}, error) {
		// Re-check inside the flight slot. Not counted as a hit;
		// see httpCache.getOrFetch for the rationale.
		sessionsMu.RLock()
		if e, ok := sessionsCached[key]; ok && !time.Now().After(e.expiresAt) {
			sessionsMu.RUnlock()
			return e.sessions, nil
		}
		sessionsMu.RUnlock()

		sessions, err := d.GetSessions(directory, 0)
		if err != nil {
			return nil, err
		}
		sessionsMu.Lock()
		sessionsCached[key] = sessionsEntry{
			sessions:  sessions,
			expiresAt: time.Now().Add(sessionsTTL),
		}
		sessionsMu.Unlock()
		return sessions, nil
	})
	if err != nil {
		return nil, err
	}
	sessions, _ := v.([]db.Session)
	return filterSessionsBySince(sessions, since), nil
}

// filterSessionsBySince returns the subset of `sessions` whose
// TimeUpdated is >= since. When since <= 0 the input slice is
// returned unchanged (no allocation). The input is sorted by
// time_updated DESC so we could short-circuit on the first
// non-matching row, but a linear scan over a few hundred rows
// is below the noise floor and the explicit filter is easier
// to reason about under future query changes.
func filterSessionsBySince(sessions []db.Session, since int64) []db.Session {
	if since <= 0 {
		return sessions
	}
	out := make([]db.Session, 0, len(sessions))
	for _, s := range sessions {
		if s.TimeUpdated >= since {
			out = append(out, s)
		}
	}
	return out
}
