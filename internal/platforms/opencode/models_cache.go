package opencode

import (
	"strconv"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/NoUseFreak/ocman/internal/db"
)

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
	recentModelsMu.RLock()
	if !time.Now().After(recentModelsCached.expiresAt) {
		out := recentModelsCached.models
		recentModelsMu.RUnlock()
		return out, nil
	}
	recentModelsMu.RUnlock()

	v, err, _ := recentModelsFlight.Do("recents", func() (interface{}, error) {
		// Re-check inside the flight slot; another caller may have
		// just refilled the cache while we were queuing.
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
	key := sessionDefaultsKey{excludeSessionID: excludeSessionID, directory: directory}

	sessionDefaultsMu.RLock()
	if e, ok := sessionDefaultsCached[key]; ok && !time.Now().After(e.expiresAt) {
		sessionDefaultsMu.RUnlock()
		return e.defaults, nil
	}
	sessionDefaultsMu.RUnlock()

	flightKey := excludeSessionID + "|" + directory
	v, err, _ := sessionDefaultsFlight.Do(flightKey, func() (interface{}, error) {
		// Re-check inside the flight slot.
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
// is keyed by (directory, since) so directory-filtered listings
// (the project drill-down view) and the global listing are
// independent. Subsequent stats overlay (live connection,
// pending prompts) still runs uncached because it depends on
// transient OpenCode HTTP state that's already cached at
// finer granularity (pendingPromptCache, port discovery).

const sessionsTTL = 3 * time.Second

type sessionsKey struct {
	directory string
	since     int64
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
// d.GetSessions. Concurrent callers on the same key share a single
// underlying query via singleflight.
//
// The returned slice is shared with other cache readers; callers
// must NOT mutate it. The OpenCode adapter only ever appends
// per-session metadata to a copy of each Session struct, never
// to the slice itself, so this is safe in practice. If a future
// caller needs to mutate the result, copy it first.
func getSessionsCached(d dbGetSessions, directory string, since int64) ([]db.Session, error) {
	key := sessionsKey{directory: directory, since: since}

	sessionsMu.RLock()
	if e, ok := sessionsCached[key]; ok && !time.Now().After(e.expiresAt) {
		sessionsMu.RUnlock()
		return e.sessions, nil
	}
	sessionsMu.RUnlock()

	flightKey := directory + "|" + strconv.FormatInt(since, 10)
	v, err, _ := sessionsFlight.Do(flightKey, func() (interface{}, error) {
		// Re-check inside the flight slot.
		sessionsMu.RLock()
		if e, ok := sessionsCached[key]; ok && !time.Now().After(e.expiresAt) {
			sessionsMu.RUnlock()
			return e.sessions, nil
		}
		sessionsMu.RUnlock()

		sessions, err := d.GetSessions(directory, since)
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
	return sessions, nil
}
