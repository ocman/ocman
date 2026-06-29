package opencode

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
)

// fakeRecentModelsDB counts calls so tests can assert the cache is
// actually preventing repeated DB hits.
type fakeRecentModelsDB struct {
	calls atomic.Int64
	out   []db.RecentModel
	err   error
}

func (f *fakeRecentModelsDB) GetRecentModels(sessionLimit, maxResults int) ([]db.RecentModel, error) {
	f.calls.Add(1)
	return f.out, f.err
}

// resetRecentModelsCache wipes the package-level cache so each test
// starts from a known-cold state. Safe to call concurrently with
// other tests *only* if those tests also call this — which they do.
func resetRecentModelsCache() {
	recentModelsMu.Lock()
	recentModelsCached = recentModelsEntry{}
	recentModelsMu.Unlock()
}

func TestGetRecentModelsCached_CachesAcrossCalls(t *testing.T) {
	resetRecentModelsCache()
	t.Cleanup(resetRecentModelsCache)

	want := []db.RecentModel{{Provider: "anthropic", Model: "claude-opus-4"}}
	d := &fakeRecentModelsDB{out: want}

	for i := 0; i < 5; i++ {
		got, err := getRecentModelsCached(d)
		if err != nil {
			t.Fatalf("call %d: unexpected error: %v", i, err)
		}
		if len(got) != 1 || got[0].Model != "claude-opus-4" {
			t.Fatalf("call %d: unexpected result: %#v", i, got)
		}
	}
	if c := d.calls.Load(); c != 1 {
		t.Errorf("expected 1 DB call across 5 cached requests, got %d", c)
	}
}

func TestGetRecentModelsCached_ConcurrentRequestsCoalesce(t *testing.T) {
	resetRecentModelsCache()
	t.Cleanup(resetRecentModelsCache)

	d := &fakeRecentModelsDB{out: []db.RecentModel{{Model: "m"}}}

	const N = 32
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := getRecentModelsCached(d); err != nil {
				t.Errorf("getRecentModelsCached: %v", err)
			}
		}()
	}
	wg.Wait()

	// Singleflight may admit a small number of separate calls if
	// the first one finishes before later goroutines reach Do, but
	// we should be way below N for this to be useful at all.
	if c := d.calls.Load(); c >= int64(N) {
		t.Errorf("expected singleflight coalescing, got %d calls for %d concurrent requests", c, N)
	}
}

func TestGetRecentModelsCached_DoesNotCacheErrors(t *testing.T) {
	resetRecentModelsCache()
	t.Cleanup(resetRecentModelsCache)

	wantErr := errors.New("db dead")
	d := &fakeRecentModelsDB{err: wantErr}

	if _, err := getRecentModelsCached(d); !errors.Is(err, wantErr) {
		t.Fatalf("expected wantErr, got %v", err)
	}
	// A transient error must not be cached: the next call must hit
	// the underlying DB again.
	if _, err := getRecentModelsCached(d); !errors.Is(err, wantErr) {
		t.Fatalf("second call: expected wantErr, got %v", err)
	}
	if c := d.calls.Load(); c != 2 {
		t.Errorf("expected 2 DB calls when errors are not cached, got %d", c)
	}
}

func TestGetRecentModelsCached_ExpiresAfterTTL(t *testing.T) {
	resetRecentModelsCache()
	t.Cleanup(resetRecentModelsCache)

	d := &fakeRecentModelsDB{out: []db.RecentModel{{Model: "m"}}}
	if _, err := getRecentModelsCached(d); err != nil {
		t.Fatalf("first call: %v", err)
	}

	// Forcibly expire the cache so we don't have to sleep for the
	// real TTL in tests.
	recentModelsMu.Lock()
	recentModelsCached.expiresAt = time.Now().Add(-time.Second)
	recentModelsMu.Unlock()

	if _, err := getRecentModelsCached(d); err != nil {
		t.Fatalf("second call: %v", err)
	}
	if c := d.calls.Load(); c != 2 {
		t.Errorf("expected 2 DB calls across the TTL boundary, got %d", c)
	}
}

// fakeSessionDefaultsDB counts GetSessionDefaults calls. Records the
// args of the most recent call so tests can verify the cache key
// shape (we want different (excludeSessionID, directory) pairs to be
// independent cache slots).
type fakeSessionDefaultsDB struct {
	calls atomic.Int64
	out   db.SessionDefaults
	err   error
}

func (f *fakeSessionDefaultsDB) GetSessionDefaults(sessionID, directory string) (db.SessionDefaults, error) {
	f.calls.Add(1)
	return f.out, f.err
}

func resetSessionDefaultsCache() {
	sessionDefaultsMu.Lock()
	sessionDefaultsCached = map[sessionDefaultsKey]sessionDefaultsEntry{}
	sessionDefaultsMu.Unlock()
}

func TestGetSessionDefaultsCached_CachesAcrossCalls(t *testing.T) {
	resetSessionDefaultsCache()
	t.Cleanup(resetSessionDefaultsCache)

	want := db.SessionDefaults{Agent: "build", Model: "anthropic/claude-opus-4"}
	d := &fakeSessionDefaultsDB{out: want}

	for i := 0; i < 5; i++ {
		got, err := getSessionDefaultsCached(d, "s1", "/repo")
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		if got != want {
			t.Fatalf("call %d: got %#v, want %#v", i, got, want)
		}
	}
	if c := d.calls.Load(); c != 1 {
		t.Errorf("expected 1 DB call across 5 cached requests, got %d", c)
	}
}

func TestGetSessionDefaultsCached_KeyDistinguishesDirectory(t *testing.T) {
	resetSessionDefaultsCache()
	t.Cleanup(resetSessionDefaultsCache)

	d := &fakeSessionDefaultsDB{out: db.SessionDefaults{Model: "m"}}

	if _, err := getSessionDefaultsCached(d, "s1", "/repo-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := getSessionDefaultsCached(d, "s1", "/repo-b"); err != nil {
		t.Fatal(err)
	}
	if _, err := getSessionDefaultsCached(d, "s1", "/repo-a"); err != nil {
		t.Fatal(err)
	}
	// Two distinct directories => two DB hits; the third call hits
	// the cache.
	if c := d.calls.Load(); c != 2 {
		t.Errorf("expected 2 DB calls (one per directory), got %d", c)
	}
}

func TestGetSessionDefaultsCached_KeyDistinguishesSessionExclusion(t *testing.T) {
	resetSessionDefaultsCache()
	t.Cleanup(resetSessionDefaultsCache)

	d := &fakeSessionDefaultsDB{out: db.SessionDefaults{Model: "m"}}

	if _, err := getSessionDefaultsCached(d, "s1", "/repo"); err != nil {
		t.Fatal(err)
	}
	if _, err := getSessionDefaultsCached(d, "s2", "/repo"); err != nil {
		t.Fatal(err)
	}
	if _, err := getSessionDefaultsCached(d, "s1", "/repo"); err != nil {
		t.Fatal(err)
	}
	// Different excludeSessionID => different cache slots, since
	// the underlying query result depends on which session's
	// messages are excluded.
	if c := d.calls.Load(); c != 2 {
		t.Errorf("expected 2 DB calls (one per excludeSessionID), got %d", c)
	}
}

func TestGetSessionDefaultsCached_ConcurrentRequestsCoalesce(t *testing.T) {
	resetSessionDefaultsCache()
	t.Cleanup(resetSessionDefaultsCache)

	d := &fakeSessionDefaultsDB{out: db.SessionDefaults{Model: "m"}}

	const N = 32
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := getSessionDefaultsCached(d, "s1", "/repo"); err != nil {
				t.Errorf("getSessionDefaultsCached: %v", err)
			}
		}()
	}
	wg.Wait()

	if c := d.calls.Load(); c >= int64(N) {
		t.Errorf("expected singleflight coalescing, got %d calls for %d concurrent requests", c, N)
	}
}

func TestGetSessionDefaultsCached_DoesNotCacheErrors(t *testing.T) {
	resetSessionDefaultsCache()
	t.Cleanup(resetSessionDefaultsCache)

	wantErr := errors.New("db dead")
	d := &fakeSessionDefaultsDB{err: wantErr}

	if _, err := getSessionDefaultsCached(d, "s1", "/repo"); !errors.Is(err, wantErr) {
		t.Fatalf("call 1: %v", err)
	}
	if _, err := getSessionDefaultsCached(d, "s1", "/repo"); !errors.Is(err, wantErr) {
		t.Fatalf("call 2: %v", err)
	}
	if c := d.calls.Load(); c != 2 {
		t.Errorf("expected 2 DB calls when errors are not cached, got %d", c)
	}
}

func TestGetSessionDefaultsCached_ExpiresAfterTTL(t *testing.T) {
	resetSessionDefaultsCache()
	t.Cleanup(resetSessionDefaultsCache)

	d := &fakeSessionDefaultsDB{out: db.SessionDefaults{Model: "m"}}
	if _, err := getSessionDefaultsCached(d, "s1", "/repo"); err != nil {
		t.Fatalf("first call: %v", err)
	}

	// Forcibly expire the cached entry.
	key := sessionDefaultsKey{excludeSessionID: "s1", directory: "/repo"}
	sessionDefaultsMu.Lock()
	e := sessionDefaultsCached[key]
	e.expiresAt = time.Now().Add(-time.Second)
	sessionDefaultsCached[key] = e
	sessionDefaultsMu.Unlock()

	if _, err := getSessionDefaultsCached(d, "s1", "/repo"); err != nil {
		t.Fatalf("second call: %v", err)
	}
	if c := d.calls.Load(); c != 2 {
		t.Errorf("expected 2 DB calls across the TTL boundary, got %d", c)
	}
}

// fakeGetSessionsDB counts GetSessions calls and returns a fixed
// slice. The slice is intentionally shared between calls so tests
// can assert that getSessionsCached doesn't accidentally hand out
// a slice that the caller has already mutated. lastSince records
// the most recent `since` argument so tests can assert the cache
// always queries the unfiltered (since=0) superset.
type fakeGetSessionsDB struct {
	calls     atomic.Int64
	lastSince atomic.Int64
	out       []db.Session
	err       error
}

func (f *fakeGetSessionsDB) GetSessions(directory string, since int64) ([]db.Session, error) {
	f.calls.Add(1)
	f.lastSince.Store(since)
	return f.out, f.err
}

func resetSessionsCache() {
	sessionsMu.Lock()
	sessionsCached = map[sessionsKey]sessionsEntry{}
	sessionsMu.Unlock()
}

func TestGetSessionsCached_CachesAcrossCalls(t *testing.T) {
	resetSessionsCache()
	t.Cleanup(resetSessionsCache)

	want := []db.Session{{ID: "s1"}, {ID: "s2"}}
	d := &fakeGetSessionsDB{out: want}

	for i := 0; i < 5; i++ {
		got, err := getSessionsCached(d, "", 0)
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		if len(got) != 2 || got[0].ID != "s1" {
			t.Fatalf("call %d: unexpected: %#v", i, got)
		}
	}
	if c := d.calls.Load(); c != 1 {
		t.Errorf("expected 1 DB call across 5 cached requests, got %d", c)
	}
}

func TestInvalidateSessionsCache_ForcesRefetch(t *testing.T) {
	resetSessionsCache()
	t.Cleanup(resetSessionsCache)

	d := &fakeGetSessionsDB{out: []db.Session{{ID: "s1"}}}

	if _, err := getSessionsCached(d, "", 0); err != nil {
		t.Fatal(err)
	}
	// Second call within TTL would normally hit cache (no extra DB call).
	if _, err := getSessionsCached(d, "", 0); err != nil {
		t.Fatal(err)
	}
	if c := d.calls.Load(); c != 1 {
		t.Fatalf("expected 1 DB call before invalidation, got %d", c)
	}

	InvalidateSessionsCache()

	// After invalidation the next read must refetch.
	if _, err := getSessionsCached(d, "", 0); err != nil {
		t.Fatal(err)
	}
	if c := d.calls.Load(); c != 2 {
		t.Errorf("expected 2 DB calls after invalidation, got %d", c)
	}
}

func TestGetSessionsCached_KeyDistinguishesDirectoryOnly(t *testing.T) {
	resetSessionsCache()
	t.Cleanup(resetSessionsCache)

	d := &fakeGetSessionsDB{out: []db.Session{{ID: "s", TimeUpdated: 1000}}}

	if _, err := getSessionsCached(d, "/a", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := getSessionsCached(d, "/b", 0); err != nil {
		t.Fatal(err)
	}
	// A different `since` value MUST NOT mint a new cache slot —
	// otherwise a frontend poller using a rolling
	// `Date.now() - LOOKBACK` would leak one map entry per poll.
	if _, err := getSessionsCached(d, "/a", 100); err != nil {
		t.Fatal(err)
	}
	if _, err := getSessionsCached(d, "/a", 999); err != nil {
		t.Fatal(err)
	}
	if _, err := getSessionsCached(d, "/a", 0); err != nil {
		t.Fatal(err)
	}
	// 2 distinct directories -> 2 DB calls; all the /a calls
	// regardless of `since` share one cache slot.
	if c := d.calls.Load(); c != 2 {
		t.Errorf("expected 2 DB calls (one per directory), got %d", c)
	}
}

func TestGetSessionsCached_AlwaysFetchesUnfiltered(t *testing.T) {
	resetSessionsCache()
	t.Cleanup(resetSessionsCache)

	d := &fakeGetSessionsDB{out: []db.Session{{ID: "s", TimeUpdated: 1000}}}

	// Caller passes a non-zero `since`, but the cache must fetch
	// the unfiltered superset so subsequent callers with smaller
	// `since` values still see all rows.
	if _, err := getSessionsCached(d, "/repo", 500); err != nil {
		t.Fatal(err)
	}
	if got := d.lastSince.Load(); got != 0 {
		t.Errorf("expected GetSessions called with since=0, got since=%d", got)
	}
}

func TestGetSessionsCached_PostFiltersBySince(t *testing.T) {
	resetSessionsCache()
	t.Cleanup(resetSessionsCache)

	// Three sessions spanning a wide time range.
	d := &fakeGetSessionsDB{out: []db.Session{
		{ID: "new", TimeUpdated: 2000},
		{ID: "mid", TimeUpdated: 1500},
		{ID: "old", TimeUpdated: 500},
	}}

	// First call warms the cache (since=0 -> all three).
	all, err := getSessionsCached(d, "/repo", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("warm: expected 3 sessions, got %d", len(all))
	}

	// Second call with since=1000 must filter to {new, mid} from
	// the cached slice — and must NOT incur a second DB call.
	got, err := getSessionsCached(d, "/repo", 1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "new" || got[1].ID != "mid" {
		t.Fatalf("filtered: unexpected result: %#v", got)
	}
	if c := d.calls.Load(); c != 1 {
		t.Errorf("expected 1 DB call (cache hit on filter), got %d", c)
	}

	// Third call with a since past every row returns empty.
	got, err = getSessionsCached(d, "/repo", 9999)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("filter-all: expected empty, got %#v", got)
	}
}

func TestGetSessionsCached_ConcurrentRequestsCoalesce(t *testing.T) {
	resetSessionsCache()
	t.Cleanup(resetSessionsCache)

	d := &fakeGetSessionsDB{out: []db.Session{{ID: "s"}}}

	const N = 32
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := getSessionsCached(d, "/repo", 0); err != nil {
				t.Errorf("getSessionsCached: %v", err)
			}
		}()
	}
	wg.Wait()

	if c := d.calls.Load(); c >= int64(N) {
		t.Errorf("expected singleflight coalescing, got %d calls for %d concurrent requests", c, N)
	}
}

func TestGetSessionsCached_DoesNotCacheErrors(t *testing.T) {
	resetSessionsCache()
	t.Cleanup(resetSessionsCache)

	wantErr := errors.New("db dead")
	d := &fakeGetSessionsDB{err: wantErr}

	if _, err := getSessionsCached(d, "/repo", 0); !errors.Is(err, wantErr) {
		t.Fatalf("call 1: %v", err)
	}
	if _, err := getSessionsCached(d, "/repo", 0); !errors.Is(err, wantErr) {
		t.Fatalf("call 2: %v", err)
	}
	if c := d.calls.Load(); c != 2 {
		t.Errorf("expected 2 DB calls when errors are not cached, got %d", c)
	}
}

// failableGetSessionsDB serves out/err that the test can flip at
// runtime, so we can warm the cache and then simulate the DB stalling
// out (busy_timeout) on a later refresh.
type failableGetSessionsDB struct {
	mu  sync.Mutex
	out []db.Session
	err error
}

func (f *failableGetSessionsDB) GetSessions(_ string, _ int64) ([]db.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.out, f.err
}

func (f *failableGetSessionsDB) setErr(err error) {
	f.mu.Lock()
	f.err = err
	f.mu.Unlock()
}

// TestGetSessionsCached_ServesStaleOnError is the core of the slow-call
// fix: when the underlying query fails (in production: stalls on the
// WAL busy_timeout), a poll with a previously-cached value must return
// that stale value instead of blocking/erroring.
func TestGetSessionsCached_ServesStaleOnError(t *testing.T) {
	resetSessionsCache()
	t.Cleanup(resetSessionsCache)

	d := &failableGetSessionsDB{out: []db.Session{{ID: "good"}}}

	// Warm the cache with a good value.
	if _, err := getSessionsCached(d, "/repo", 0); err != nil {
		t.Fatalf("warm: %v", err)
	}

	// Expire the entry and make the DB start failing.
	key := sessionsKey{directory: "/repo"}
	sessionsMu.Lock()
	e := sessionsCached[key]
	e.expiresAt = time.Now().Add(-time.Second)
	sessionsCached[key] = e
	sessionsMu.Unlock()
	d.setErr(errors.New("database is locked"))

	got, err := getSessionsCached(d, "/repo", 0)
	if err != nil {
		t.Fatalf("expected stale value, got error: %v", err)
	}
	if len(got) != 1 || got[0].ID != "good" {
		t.Fatalf("expected stale 'good' value, got %#v", got)
	}
}

func TestGetSessionsCached_ExpiresAfterTTL(t *testing.T) {
	resetSessionsCache()
	t.Cleanup(resetSessionsCache)

	d := &fakeGetSessionsDB{out: []db.Session{{ID: "s"}}}
	if _, err := getSessionsCached(d, "/repo", 0); err != nil {
		t.Fatalf("first call: %v", err)
	}

	// Force the cached entry to expire.
	key := sessionsKey{directory: "/repo"}
	sessionsMu.Lock()
	e := sessionsCached[key]
	e.expiresAt = time.Now().Add(-time.Second)
	sessionsCached[key] = e
	sessionsMu.Unlock()

	if _, err := getSessionsCached(d, "/repo", 0); err != nil {
		t.Fatalf("second call: %v", err)
	}
	if c := d.calls.Load(); c != 2 {
		t.Errorf("expected 2 DB calls across the TTL boundary, got %d", c)
	}
}

// waitFor polls cond until it returns true or the deadline passes,
// failing the test on timeout. Avoids fixed sleeps tied to the
// refresh interval.
func waitFor(t *testing.T, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}

func TestStartSessionsRefresher_WarmsCacheOnStart(t *testing.T) {
	resetSessionsCache()
	t.Cleanup(resetSessionsCache)

	d := &fakeGetSessionsDB{out: []db.Session{{ID: "s1"}}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	StartSessionsRefresher(ctx, d)

	// The refresher fires one query immediately so the first request
	// after startup is already a cache hit.
	waitFor(t, time.Second, func() bool { return d.calls.Load() >= 1 })

	sessionsMu.RLock()
	e, ok := sessionsCached[sessionsKey{directory: ""}]
	sessionsMu.RUnlock()
	if !ok || len(e.sessions) != 1 || e.sessions[0].ID != "s1" {
		t.Fatalf("expected warm cache entry for the unfiltered key, got ok=%v entry=%#v", ok, e)
	}
}

func TestStartSessionsRefresher_StopsOnContextCancel(t *testing.T) {
	resetSessionsCache()
	t.Cleanup(resetSessionsCache)

	d := &fakeGetSessionsDB{out: []db.Session{{ID: "s1"}}}
	ctx, cancel := context.WithCancel(context.Background())

	StartSessionsRefresher(ctx, d)
	waitFor(t, time.Second, func() bool { return d.calls.Load() >= 1 })

	// Cancel and confirm the goroutine stops issuing queries.
	cancel()
	settled := d.calls.Load()
	time.Sleep(50 * time.Millisecond)
	after := d.calls.Load()
	// Allow at most one in-flight refresh to land after cancel.
	if after > settled+1 {
		t.Errorf("refresher kept querying after cancel: %d -> %d", settled, after)
	}
}
