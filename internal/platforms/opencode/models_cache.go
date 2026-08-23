package opencode

import (
	"context"
	"errors"
	"sort"
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

// afterTopLevelMiss is a test-only hook invoked after a cache read
// misses at the top level but before the caller enters the singleflight
// Do slot. It is nil in production (zero cost) and lets tests pin a
// caller in exactly that window so the "re-check inside the flight slot"
// branch runs deterministically instead of via a scheduling race (the
// source of the coverage jitter this closes). Set/cleared under test
// only; the caches' tests run serially per function so a single shared
// hook suffices.
var afterTopLevelMiss func()

// afterSessionsFlightJoin is a test-only hook invoked after DoChan has
// registered a caller as the leader or a follower.
var afterSessionsFlightJoin func()

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
		// One global snapshot, so this is 0 or 1 — same shape as the
		// recent_models gauge above. A stale snapshot still counts: it
		// is retained and served while revalidation runs.
		if !sessionsHave {
			return 0
		}
		return 1
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
	GetRecentModels(context.Context, int, int) ([]db.RecentModel, error)
}

// getRecentModelsCached returns a (possibly cached) result of
// d.GetRecentModels(50, 10). Concurrent callers on a cold cache are
// coalesced via singleflight so only one DB scan runs.
func getRecentModelsCached(ctx context.Context, d dbRecentModels) ([]db.RecentModel, error) {
	recentModelsMu.RLock()
	if !time.Now().After(recentModelsCached.expiresAt) {
		out := recentModelsCached.models
		recentModelsMu.RUnlock()
		recentModelsMetrics.RecordHit(ctx)
		return out, nil
	}
	recentModelsMu.RUnlock()
	recentModelsMetrics.RecordMiss(ctx)
	if afterTopLevelMiss != nil {
		afterTopLevelMiss()
	}

	result := recentModelsFlight.DoChan("recents", func() (interface{}, error) {
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

		models, err := d.GetRecentModels(context.WithoutCancel(ctx), 50, 10)
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
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-result:
		if result.Err != nil {
			return nil, result.Err
		}
		models, _ := result.Val.([]db.RecentModel)
		return models, nil
	}
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
	GetSessionDefaults(context.Context, string, string) (db.SessionDefaults, error)
}

// getSessionDefaultsCached returns a (possibly cached) result of
// d.GetSessionDefaults. Cache misses on the same key are coalesced
// via singleflight so a SessionDetail mount fan-out runs the
// expensive join exactly once.
func getSessionDefaultsCached(ctx context.Context, d dbSessionDefaults, excludeSessionID, directory string) (db.SessionDefaults, error) {
	key := sessionDefaultsKey{excludeSessionID: excludeSessionID, directory: directory}

	sessionDefaultsMu.RLock()
	if e, ok := sessionDefaultsCached[key]; ok && !time.Now().After(e.expiresAt) {
		sessionDefaultsMu.RUnlock()
		sessionDefaultsMetrics.RecordHit(ctx)
		return e.defaults, nil
	}
	sessionDefaultsMu.RUnlock()
	sessionDefaultsMetrics.RecordMiss(ctx)
	if afterTopLevelMiss != nil {
		afterTopLevelMiss()
	}

	flightKey := excludeSessionID + "|" + directory
	result := sessionDefaultsFlight.DoChan(flightKey, func() (interface{}, error) {
		// Re-check inside the flight slot. Not counted as a hit;
		// see httpCache.getOrFetch for the rationale.
		sessionDefaultsMu.RLock()
		if e, ok := sessionDefaultsCached[key]; ok && !time.Now().After(e.expiresAt) {
			sessionDefaultsMu.RUnlock()
			return e.defaults, nil
		}
		sessionDefaultsMu.RUnlock()

		defaults, err := d.GetSessionDefaults(context.WithoutCancel(ctx), excludeSessionID, directory)
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
	select {
	case <-ctx.Done():
		return db.SessionDefaults{}, ctx.Err()
	case result := <-result:
		if result.Err != nil {
			return db.SessionDefaults{}, result.Err
		}
		defaults, _ := result.Val.(db.SessionDefaults)
		return defaults, nil
	}
}

// db.GetSessions runs seven correlated json_extract subqueries
// against a multi-thousand-row session table joined with a
// multi-tens-of-thousands-row message table. On a representative
// developer DB it costs 250–290 ms per call. The dashboard polls
// /api/sessions and /api/sessions/notify (which both call
// GetSessions) every few seconds, so without caching we pay the
// full cost on every poll.
//
// The TTL (sessionsTTL, below) is the trade-off: short enough
// that "I started a new session in another tab" feels instant,
// long enough that the 5s poll cycle hits the cache. One global
// snapshot serves every directory: the directory and `since`
// filters are applied to that slice in Go, so N project views
// cost N in-memory scans rather than N full aggregate queries,
// and a caller passing a rolling `Date.now() - LOOKBACK` value
// can't grow the cache at all (there is nothing to key — see the
// heap-growth investigation in spec/perf-notes). The post-filter
// is cheap: a couple of compares per row over a slice that's
// already in memory.
//
// Subsequent stats overlay (live connection, pending prompts)
// still runs uncached because it depends on transient OpenCode
// HTTP state that's already cached at finer granularity
// (port discovery).

// sessionsTTL must exceed the frontend poll interval (5s for the
// dashboard/project views, ~10s for notify) or every poll lands on
// an expired entry and pays the full ~5s GetSessions query.
const sessionsTTL = 15 * time.Second

// sessionsReconcileInterval is the longest the snapshot may go without
// a full aggregate scan. Between reconciliations the refresher only
// recomputes sessions the event stream marked dirty, which is what
// takes the ~4.3 s scan on a 12 GB database off the every-few-seconds
// path (~168 scans/hour down to ~12).
//
// It exists because incremental refresh is only as complete as the
// event stream: a missed event, an event ocman cannot attribute, or a
// change OpenCode does not announce at all would otherwise never be
// corrected. Correctness therefore never depends on events or
// timestamps — it depends on this scan, and the events only decide how
// quickly a change is picked up in between.
const sessionsReconcileInterval = 5 * time.Minute

const sessionsDemandRetry = 4 * time.Second

// sessionsFlightKey is the single singleflight slot for the snapshot
// refresh. One constant key is correct because there is exactly one
// snapshot; see the one-database note on sessionsSnapshot.
const sessionsFlightKey = "sessions"

var (
	sessionsMu sync.RWMutex
	// sessionsSnapshot is THE session list: one global, unfiltered
	// []db.Session that every read filters in memory. It is a package
	// global rather than a keyed cache because ocman opens exactly one
	// local OpenCode database per process (AGENTS.md, "Two databases"),
	// so a per-source key would only ever hold one entry. Keying it by
	// the dbGetSessions value would additionally panic for a
	// non-comparable implementation.
	//
	// Three states, in order of how much a read is allowed to do:
	//
	//	!sessionsHave                  cold  — read waits, error surfaces
	//	expired                        stale — read serves this snapshot,
	//	                                       one background refresh runs
	//	expired && sessionsInvalidated dirty — read blocks on a fresh
	//	                                       fetch (falling back to this
	//	                                       snapshot only if it fails)
	//
	// The dirty state exists because TTL expiry and explicit
	// invalidation are not the same event. Expiry says "this might be
	// slightly old", which is what stale-while-revalidate is for.
	// InvalidateSessionsCache says "something changed" — a session
	// created in another tab or by an upstream event — and serving the
	// old snapshot there would hide the new session until a later poll.
	// A successful refresh clears sessionsInvalidated, so ordinary TTL
	// expiry goes back to the non-blocking path.
	//
	// Orthogonal to those three: sessionsDirty/sessionsFullDirty say
	// *what* a refresh has to recompute. A snapshot can be fresh and
	// dirty at once — the rows we hold are recent, but the event stream
	// has told us which of them moved. That read still returns
	// immediately and recomputes those rows behind the response.
	//
	// A failed refresh leaves all of these untouched, which is what
	// keeps the last good value serving and the refresh retryable.
	sessionsSnapshot    []db.Session
	sessionsHave        bool
	sessionsExpiresAt   time.Time
	sessionsInvalidated bool
	sessionsFlight      singleflight.Group
	// sessionsRefreshWG tracks the refresh goroutines this package
	// spawns (the background revalidation and the refresher loop) so
	// tests can drain them deterministically instead of sleeping.
	// Production never waits on it.
	sessionsRefreshWG sync.WaitGroup
	// lastRefreshEnd / lastRefreshCost record when the most recent
	// successful unfiltered refresh finished and what it cost. Both are
	// guarded by sessionsMu. They exist so the query's own cost can rate
	// limit how often it runs: on a multi-GB OpenCode DB a pass takes
	// seconds, and anything that triggers passes faster than that pins a
	// core and exhausts the read pool.
	lastRefreshEnd  time.Time
	lastRefreshCost time.Duration
	// sessionsDirty holds the IDs of sessions whose row may have
	// changed, as reported by the OpenCode event stream. The refresher
	// drains it by recomputing exactly those rows (db.GetSessionSummary
	// runs the same projection GetSessions does) and merging them into
	// the snapshot. sessionsFullDirty is the fail-safe for events that
	// cannot be attributed to a single session: it forces the next pass
	// to be a full scan rather than let the pass guess. lastFullRefresh
	// is when the last full scan finished, which is what schedules the
	// periodic reconciliation. All three are guarded by sessionsMu.
	sessionsDirty       = map[string]struct{}{}
	sessionsFullDirty   bool
	lastFullRefresh     time.Time
	sessionsRefreshWake = make(chan struct{}, 1)
)

// MarkSessionDirty records that one session's row may have changed, so
// the next refresh recomputes just that session instead of rescanning
// the database. Safe to call at event rate: it is one map insert, and
// repeat marks for the same session collapse into one recomputation.
//
// It deliberately does not expire the snapshot. Reads keep serving the
// last good rows immediately; the refresh happens behind them.
func MarkSessionDirty(sessionID string) {
	if sessionID == "" {
		return
	}
	sessionsMu.Lock()
	sessionsDirty[sessionID] = struct{}{}
	sessionsMu.Unlock()
	signalSessionsRefresh()
}

// MarkSessionsDirty marks the whole snapshot dirty, forcing the next
// refresh to be a full scan. This is the honest answer for an event
// that says something changed but not what: approximating which
// sessions it touched would put wrong values in front of the user,
// where a full scan only costs time.
func MarkSessionsDirty() {
	sessionsMu.Lock()
	sessionsFullDirty = true
	sessionsMu.Unlock()
	signalSessionsRefresh()
}

func signalSessionsRefresh() {
	select {
	case sessionsRefreshWake <- struct{}{}:
	default:
	}
}

// takeDirty claims the pending dirty work and clears it. Anything
// marked after this returns stays queued for the next pass, so a change
// that lands while a refresh runs is never cleared by that refresh.
// Callers must hold sessionsMu.
func takeDirty() (ids []string, full bool) {
	full = sessionsFullDirty
	sessionsFullDirty = false
	ids = make([]string, 0, len(sessionsDirty))
	for id := range sessionsDirty {
		ids = append(ids, id)
	}
	sessionsDirty = map[string]struct{}{}
	return ids, full
}

// restoreDirty puts claimed work back after a failed refresh so the
// next pass retries it. Callers must not hold sessionsMu.
func restoreDirty(ids []string, full bool) {
	sessionsMu.Lock()
	for _, id := range ids {
		sessionsDirty[id] = struct{}{}
	}
	if full {
		sessionsFullDirty = true
	}
	sessionsMu.Unlock()
}

// StartSessionsRefresher warms the unfiltered sessions cache when sessions
// are in demand, refreshes it when events mark work dirty, and performs the
// five-minute full reconciliation while demand remains. It shares the
// request path's singleflight slot. A nil demand callback always runs.
//
// A pass is incremental: it recomputes the sessions the event stream
// marked dirty and merges them. It escalates to a full scan when there
// is no snapshot yet, when something unattributable changed, or when
// the periodic reconciliation is due, so an idle machine does no work
// between reconciliations and a missed event is still corrected within
// sessionsReconcileInterval.
func StartSessionsRefresher(ctx context.Context, d dbGetSessions, hasDemand func(string) bool) {
	sessionsRefreshWG.Add(1)
	go func() {
		defer sessionsRefreshWG.Done()
		delay := sessionsDemandRetry
		if hasDemand == nil || hasDemand("sessions") {
			// Warm immediately so the first request after startup hits cache.
			// The snapshot is cold, so this pass is a full scan.
			refreshSessionsIncremental(ctx, d)
			delay = nextSessionsReconcileDelay()
		}
		timer := time.NewTimer(delay)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-sessionsRefreshWake:
				if hasDemand != nil && !hasDemand("sessions") {
					resetTimer(timer, sessionsDemandRetry)
					continue
				}
				if !waitForSessionsRefreshBudget(ctx) {
					return
				}
				refreshSessionsIncremental(ctx, d)
				resetTimer(timer, nextSessionsReconcileDelay())
			case <-timer.C:
				if hasDemand != nil && !hasDemand("sessions") {
					resetTimer(timer, sessionsDemandRetry)
					continue
				}
				refreshSessionsIncremental(ctx, d)
				resetTimer(timer, nextSessionsReconcileDelay())
			}
		}
	}()
}

func waitForSessionsRefreshBudget(ctx context.Context) bool {
	sessionsMu.RLock()
	delay := time.Until(lastRefreshEnd.Add(lastRefreshCost))
	sessionsMu.RUnlock()
	if delay <= 0 {
		return true
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func nextSessionsReconcileDelay() time.Duration {
	sessionsMu.RLock()
	defer sessionsMu.RUnlock()
	delay := time.Until(lastFullRefresh.Add(sessionsReconcileInterval))
	if delay < 0 {
		return 0
	}
	return delay
}

func resetTimer(timer *time.Timer, delay time.Duration) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(delay)
}

// invalidationFloor is the earliest an invalidated entry may expire:
// one query duration after the last refresh finished. Returning a time
// in the past (the common case — nothing has refreshed recently) means
// "expire now". It only defers when a pass just finished, which is what
// stops a burst of session.updated events from chaining full scans
// back-to-back. Callers must hold sessionsMu.
func invalidationFloor() time.Time {
	return lastRefreshEnd.Add(lastRefreshCost)
}

// InvalidateSessionsCache marks the cached sessions snapshot as expired
// so the next getSessionsCached read fetches fresh data. It does NOT
// delete the entries: the last good value is retained for the
// stale-on-busy fallback if the fresh fetch stalls on the live DB.
// Used as the fallback when an exact session refresh fails, so the next
// request reconciles the full snapshot instead of waiting out sessionsTTL.
//
// The new expiry is floored at invalidationFloor rather than set to the
// zero time. Every first-seen session ID invalidates, and with several
// OpenCode instances (each spawning subagents) that is a steady stream —
// unfloored, each one dropped straight into another multi-second full
// scan, so the query ran continuously no matter what the refresher's own
// pacing said. The floor costs at most one query duration of extra lag
// before a new session appears, which is the floor of what's achievable
// anyway.
func InvalidateSessionsCache() {
	sessionsMu.Lock()
	// Only ever bring the expiry forward, never push it back.
	if floor := invalidationFloor(); sessionsHave && floor.Before(sessionsExpiresAt) {
		sessionsExpiresAt = floor
	}
	// Mark it dirty, not merely stale: the next read that finds this
	// snapshot expired must block on a fresh fetch rather than serve it
	// and revalidate in the background. Set even while the floor still
	// holds the snapshot fresh, so the fetch happens as soon as the
	// floor passes.
	sessionsInvalidated = true
	sessionsMu.Unlock()
}

// ResetCachesForTests clears every package-level cache (recents,
// session defaults, sessions list). Call this from integration tests
// that seed a fresh DB so a previous test's cache doesn't leak in.
//
// Exported (capitalised) so the server-package integration tests can
// reach it without a circular import. Production code never needs it
// — the TTLs handle eventual freshness on their own.
func ResetCachesForTests() {
	// Wait out any background refresh first, or it would repopulate the
	// snapshot we are about to clear.
	sessionsRefreshWG.Wait()

	recentModelsMu.Lock()
	recentModelsCached = recentModelsEntry{}
	recentModelsMu.Unlock()

	sessionDefaultsMu.Lock()
	sessionDefaultsCached = map[sessionDefaultsKey]sessionDefaultsEntry{}
	sessionDefaultsMu.Unlock()

	sessionsMu.Lock()
	sessionsSnapshot = nil
	sessionsHave = false
	sessionsExpiresAt = time.Time{}
	sessionsInvalidated = false
	sessionsDirty = map[string]struct{}{}
	sessionsFullDirty = false
	lastRefreshEnd = time.Time{}
	lastRefreshCost = 0
	lastFullRefresh = time.Time{}
	sessionsMu.Unlock()
	select {
	case <-sessionsRefreshWake:
	default:
	}
}

// dbGetSessions is the subset of *db.DB used by getSessionsCached.
// Defined as an interface so tests can stub the read.
//
// The two methods run the same projection: GetSessionSummary is
// GetSessions' expressions with an id predicate, so a row it returns is
// identical to that session's row in a full scan and can be merged
// into the snapshot without drift.
type dbGetSessions interface {
	GetSessions(context.Context, string, int64) ([]db.Session, error)
	GetSessionSummary(context.Context, string) (db.Session, error)
}

// getSessionsCached returns directory and since filtered rows from one
// global d.GetSessions("", 0) snapshot. Background refreshes coalesce,
// while a blocking request owns its query so cancellation reaches SQLite.
//
// Which of the three snapshot states applies decides whether this
// blocks; see the state table on sessionsSnapshot.
func getSessionsCached(ctx context.Context, d dbGetSessions, directory string, since int64) ([]db.Session, error) {
	// Read the snapshot and its state, fresh or stale, in one lock.
	sessionsMu.RLock()
	snapshot, have, expiresAt := sessionsSnapshot, sessionsHave, sessionsExpiresAt
	invalidated := sessionsInvalidated
	dirty := len(sessionsDirty) > 0 || sessionsFullDirty
	sessionsMu.RUnlock()

	if have && !time.Now().After(expiresAt) {
		sessionsListMetrics.RecordHit(ctx)
		if dirty {
			// Fresh but known-changed. Serve now, recompute the changed
			// sessions behind the response: the work is proportional to
			// what moved, so there is no reason to make the reader wait
			// for it.
			startBackgroundSessionsRefresh(d)
		}
		return filterSessions(snapshot, directory, since), nil
	}
	sessionsListMetrics.RecordMiss(ctx)
	if have && !invalidated {
		// Stale-while-revalidate: the last good snapshot answers now,
		// one background refresh (singleflighted, so concurrent stale
		// readers share it) replaces it later. A failed refresh leaves
		// the snapshot expired, so the next read retries.
		sessionsRefreshWG.Add(1)
		go func() {
			defer sessionsRefreshWG.Done()
			_, _ = refreshSessions(context.Background(), d)
		}()
		return filterSessions(snapshot, directory, since), nil
	}

	// Cold or explicitly invalidated: this read waits for fresh data,
	// so a session created elsewhere shows up on the very next request.
	sessions, err := refreshSessionsForRequest(ctx, d)
	if err != nil {
		// Stale-on-busy: the live OpenCode DB can stall a read on the
		// WAL busy_timeout (multi-GB file, many concurrent writers).
		// Rather than fail the poll, serve the last good value if we
		// have one; the snapshot stays invalidated, so the next read
		// tries again.
		if have {
			return filterSessions(snapshot, directory, since), nil
		}
		return nil, err
	}
	return filterSessions(sessions, directory, since), nil
}

func refreshSessionsForRequest(ctx context.Context, d dbGetSessions) ([]db.Session, error) {
	if afterTopLevelMiss != nil {
		afterTopLevelMiss()
	}
	return doSessionsFlight(ctx, true, func() ([]db.Session, error) {
		return runFullSessionsRefresh(ctx, d)
	})
}

// startBackgroundSessionsRefresh runs one refresh behind the caller.
// Tracked on sessionsRefreshWG so tests can drain it; production never
// waits. Concurrent starts coalesce in the singleflight slot.
func startBackgroundSessionsRefresh(d dbGetSessions) {
	sessionsRefreshWG.Add(1)
	go func() {
		defer sessionsRefreshWG.Done()
		_, _ = refreshSessionsIncremental(context.Background(), d)
	}()
}

// RefreshSession makes one session visible in the shared list snapshot before
// returning. It avoids the full aggregate scan used by explicit invalidation.
func RefreshSession(ctx context.Context, d *db.DB, sessionID string) error {
	return refreshSessionNow(ctx, d, sessionID)
}

func refreshSessionNow(ctx context.Context, d dbGetSessions, sessionID string) error {
	MarkSessionDirty(sessionID)
	for {
		if _, err := refreshSessionsIncremental(ctx, d); err != nil {
			return err
		}

		sessionsMu.RLock()
		_, stillDirty := sessionsDirty[sessionID]
		visible := false
		for _, session := range sessionsSnapshot {
			if session.ID == sessionID {
				visible = true
				break
			}
		}
		sessionsMu.RUnlock()
		if visible {
			return nil
		}
		if !stillDirty {
			return db.ErrSessionNotFound
		}
	}
}

// refreshSessions runs the global unfiltered GetSessions query and
// overwrites the snapshot on success. Concurrent callers coalesce
// through the one singleflight slot, so an in-flight refresh is never
// duplicated. The snapshot is only replaced on success, so a
// slow/failed fetch leaves the previous good value in place for
// getSessionsCached's stale-on-busy fallback.
func refreshSessions(ctx context.Context, d dbGetSessions) ([]db.Session, error) {
	if afterTopLevelMiss != nil {
		afterTopLevelMiss()
	}
	return doSessionsFlight(ctx, false, func() ([]db.Session, error) {
		// Re-check inside the flight slot: a concurrent caller may
		// have just refreshed it.
		sessionsMu.RLock()
		if sessionsHave && !time.Now().After(sessionsExpiresAt) {
			out := sessionsSnapshot
			sessionsMu.RUnlock()
			return out, nil
		}
		sessionsMu.RUnlock()

		return runFullSessionsRefresh(context.WithoutCancel(ctx), d)
	})
}

// refreshSessionsIncremental brings the snapshot up to date with the
// least work that is still exact. It recomputes the sessions the event
// stream marked dirty and merges them; it escalates to a full scan when
// there is nothing to merge into (cold), when a change could not be
// attributed to a session, when an explicit invalidation is pending, or
// when the periodic reconciliation is due.
//
// It shares refreshSessions' singleflight slot, so an incremental pass
// and a full one can never run against the snapshot at the same time.
func refreshSessionsIncremental(ctx context.Context, d dbGetSessions) ([]db.Session, error) {
	if afterTopLevelMiss != nil {
		afterTopLevelMiss()
	}
	return doSessionsFlight(ctx, false, func() ([]db.Session, error) {
		ctx := context.WithoutCancel(ctx)
		sessionsMu.Lock()
		ids, fullDirty := takeDirty()
		// An explicit invalidation is deliberately NOT a reason to
		// escalate to a full scan here. It is answered by the blocking
		// read path (which InvalidateSessionsCache expires the snapshot
		// for), and that path applies invalidationFloor's rate limit.
		// Escalating here would run the full scan the floor exists to
		// defer.
		full := fullDirty || !sessionsHave ||
			time.Since(lastFullRefresh) >= sessionsReconcileInterval
		snapshot := sessionsSnapshot
		sessionsMu.Unlock()

		if full {
			sessions, err := runFullSessionsRefresh(ctx, d)
			if err != nil {
				restoreDirty(ids, fullDirty)
				return nil, err
			}
			return sessions, nil
		}

		if len(ids) == 0 {
			// Nothing is known to have changed. Extend the snapshot's
			// freshness rather than let the TTL lapse into a full scan
			// that would return the rows we already hold; the periodic
			// reconciliation above is what bounds how long this can go
			// on believing the event stream.
			sessionsMu.Lock()
			extendSnapshotFreshness()
			out := sessionsSnapshot
			sessionsMu.Unlock()
			return out, nil
		}

		refreshed := make(map[string]db.Session, len(ids))
		missing := make(map[string]struct{}, len(ids))
		for _, id := range ids {
			s, err := d.GetSessionSummary(ctx, id)
			switch {
			case errors.Is(err, db.ErrSessionNotFound):
				// Deleted, or no longer part of the list (a parentless
				// subagent). Either way the snapshot must lose it.
				missing[id] = struct{}{}
			case err != nil:
				// Requeue everything, including rows already read: this
				// pass merges nothing, so none of it has landed.
				restoreDirty(ids, false)
				return nil, err
			default:
				refreshed[id] = s
			}
		}

		merged := mergeSessionRows(snapshot, refreshed, missing)
		sessionsMu.Lock()
		// Atomic swap, same as the full path: readers see the whole
		// old slice or the whole new one.
		sessionsSnapshot = merged
		sessionsHave = true
		extendSnapshotFreshness()
		sessionsMu.Unlock()
		return merged, nil
	})
}

func doSessionsFlight(ctx context.Context, waitForCanceledLeader bool, refresh func() ([]db.Session, error)) ([]db.Session, error) {
	for {
		leader := make(chan struct{})
		result := sessionsFlight.DoChan(sessionsFlightKey, func() (interface{}, error) {
			close(leader)
			return refresh()
		})
		if afterSessionsFlightJoin != nil {
			afterSessionsFlightJoin()
		}
		select {
		case <-ctx.Done():
			if waitForCanceledLeader {
				select {
				case <-leader:
					<-result
				default:
				}
			}
			return nil, ctx.Err()
		case result := <-result:
			if errors.Is(result.Err, context.Canceled) && ctx.Err() == nil {
				// The canceled leader has finished, so forgetting here cannot
				// overlap it with this live caller's retry.
				sessionsFlight.Forget(sessionsFlightKey)
				continue
			}
			if result.Err != nil {
				return nil, result.Err
			}
			sessions, _ := result.Val.([]db.Session)
			return sessions, nil
		}
	}
}

// extendSnapshotFreshness marks the snapshot current for another TTL
// after an incremental pass, so the TTL lapsing does not send the next
// read into a full scan that would return rows we already hold.
//
// It does nothing while an explicit invalidation is pending: that
// invalidation expired the snapshot precisely so the next read would
// block on fresh data, and pushing the expiry back out would swallow
// it. Only a full scan clears the invalidation. Callers must hold
// sessionsMu.
func extendSnapshotFreshness() {
	if sessionsInvalidated {
		return
	}
	sessionsExpiresAt = time.Now().Add(sessionsTTL)
}

// runFullSessionsRefresh performs the reconciliation scan and replaces
// the snapshot on success. Callers must be inside the singleflight slot.
func runFullSessionsRefresh(ctx context.Context, d dbGetSessions) ([]db.Session, error) {
	// Claim the dirty set before the scan starts: everything marked so
	// far is answered by this scan, and anything marked while it runs
	// stays queued for the next pass.
	sessionsMu.Lock()
	claimed, claimedFull := takeDirty()
	sessionsMu.Unlock()

	started := time.Now()
	sessions, err := d.GetSessions(ctx, "", 0)
	if err != nil {
		restoreDirty(claimed, claimedFull)
		return nil, err
	}
	done := time.Now()
	sessionsMu.Lock()
	// Atomic swap: readers see the whole new slice or the whole
	// old one, never a partial merge. This data is fresh, so it
	// also satisfies any pending invalidation — later TTL expiry
	// goes back to the non-blocking path.
	sessionsSnapshot = sessions
	sessionsHave = true
	sessionsExpiresAt = done.Add(sessionsTTL)
	sessionsInvalidated = false
	lastRefreshEnd = done
	lastRefreshCost = done.Sub(started)
	lastFullRefresh = done
	sessionsMu.Unlock()
	return sessions, nil
}

// mergeSessionRows replaces recomputed rows, drops rows the
// single-session read reported missing, appends sessions the snapshot
// did not have yet, and restores GetSessions' time_updated DESC order.
//
// The sort is stable, so rows sharing a timestamp keep the relative
// order the last full scan gave them; the periodic reconciliation is
// what re-derives tie order from the database.
func mergeSessionRows(snapshot []db.Session, refreshed map[string]db.Session, missing map[string]struct{}) []db.Session {
	out := make([]db.Session, 0, len(snapshot)+len(refreshed))
	merged := make(map[string]struct{}, len(refreshed))
	for _, s := range snapshot {
		if _, gone := missing[s.ID]; gone {
			continue
		}
		if updated, ok := refreshed[s.ID]; ok {
			out = append(out, updated)
			merged[s.ID] = struct{}{}
			continue
		}
		out = append(out, s)
	}
	// Whatever is left is new to the snapshot. Appended in id order so
	// two new rows sharing a timestamp land in a defined position
	// rather than one map iteration order out of many.
	added := make([]db.Session, 0, len(refreshed))
	for id, s := range refreshed {
		if _, already := merged[id]; !already {
			added = append(added, s)
		}
	}
	sort.Slice(added, func(i, j int) bool { return added[i].ID < added[j].ID })
	out = append(out, added...)
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].TimeUpdated > out[j].TimeUpdated
	})
	return out
}

// filterSessions returns a shallow copy so read-time overlays cannot mutate the
// snapshot. make preserves [] rather than nil when no rows match.
func filterSessions(sessions []db.Session, directory string, since int64) []db.Session {
	out := make([]db.Session, 0, len(sessions))
	for _, s := range sessions {
		if directory != "" && s.Directory != directory {
			continue
		}
		if since > 0 && s.TimeUpdated < since {
			continue
		}
		out = append(out, s)
	}
	return out
}
