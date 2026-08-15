package opencode

import (
	"context"
	"errors"
	"fmt"
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

// installRecheckGate wires the afterTopLevelMiss hook to deterministically
// drive a cache function's "re-check inside the flight slot" branch.
//
// The branch runs only when a caller's top-level read misses (cache
// stale) but, by the time it enters the singleflight Do slot, another
// caller has already refilled the cache. That's a three-way ordering
// that's otherwise a scheduling race — which is exactly why the branch's
// coverage flips run-to-run.
//
// The gate pins caller C in the window between its top-level miss and its
// Do entry. The test:
//
//  1. starts caller A, which misses top-level, passes the hook (fillCache
//     not yet done, so A proceeds), enters Do, and runs the real DB read;
//  2. starts caller C, which misses top-level, then blocks IN the hook;
//  3. lets A finish so it fills the cache and retires the flight key;
//  4. releases C, which now enters a fresh Do whose re-check finds the
//     cache warm and returns without a second DB read.
//
// installRecheckGate deterministically drives a cache function's
// re-check-inside-flight branch. Usage per test:
//
//	arm, cParked, releaseC, reset := installRecheckGate(t)
//	defer reset()
//	// start caller A (blocks in its DB read via a blocking* stub)
//	<-dGated.entered   // A is in the DB read; cache still empty
//	arm()              // block the NEXT caller in the hook
//	// start caller C
//	<-cParked          // C is now parked in the hook (top-level already missed)
//	close(blockA); <-aDone   // A fills cache + retires the flight key
//	releaseC(); <-cDone      // C enters a fresh Do; re-check finds cache warm
//
// The cParked handshake removes the last race: C is guaranteed to have
// passed its (missing) top-level read and be waiting in the hook before A
// fills the cache, so C's subsequent Do always takes the re-check branch.
func installRecheckGate(t *testing.T) (arm func(), cParked <-chan struct{}, releaseC func(), reset func()) {
	t.Helper()
	var (
		once   sync.Once
		mu     sync.Mutex
		armed  bool
		parked = make(chan struct{})
	)
	cGate := make(chan struct{})
	armFn := func() {
		mu.Lock()
		armed = true
		mu.Unlock()
	}
	afterTopLevelMiss = func() {
		mu.Lock()
		block := armed
		mu.Unlock()
		if block {
			close(parked) // announce C is parked (only the first armed caller)
			<-cGate       // wait until released
		}
	}
	return armFn, parked,
		func() { once.Do(func() { close(cGate) }) },
		func() { afterTopLevelMiss = nil }
}

func TestGetRecentModelsCached_RechecksInsideFlight(t *testing.T) {
	resetRecentModelsCache()
	t.Cleanup(resetRecentModelsCache)
	armGateForC, cParked, releaseC, reset := installRecheckGate(t)
	defer reset()

	// A: top-level miss, passes the (unarmed) hook, enters Do, does the
	// real DB read, fills the cache. blockA holds A inside the DB read
	// until we've armed the gate, so the cache is still empty when C
	// reads the top level.
	blockA := make(chan struct{})
	d := &fakeRecentModelsDB{out: []db.RecentModel{{Model: "m"}}}
	dGated := &blockingRecentModelsDB{
		fakeRecentModelsDB: d,
		block:              blockA,
		entered:            make(chan struct{}),
	}

	aDone := make(chan struct{})
	go func() {
		defer close(aDone)
		if _, err := getRecentModelsCached(dGated); err != nil {
			t.Errorf("caller A: %v", err)
		}
	}()
	// Wait for A to reach the DB read (it has passed the hook), then arm
	// the gate so the next caller (C) blocks in the hook.
	<-dGated.entered
	armGateForC()

	// C: top-level miss (cache still empty), then parks in the hook.
	cDone := make(chan struct{})
	go func() {
		defer close(cDone)
		got, err := getRecentModelsCached(dGated)
		if err != nil {
			t.Errorf("caller C: %v", err)
		}
		if len(got) != 1 || got[0].Model != "m" {
			t.Errorf("caller C: unexpected result %#v", got)
		}
	}()
	<-cParked // C has missed the top level and is parked in the hook.

	// Let A finish: fills cache, retires flight key.
	close(blockA)
	<-aDone
	// Now release C into a fresh Do; its re-check finds the cache warm.
	releaseC()
	<-cDone

	if c := d.calls.Load(); c != 1 {
		t.Fatalf("expected exactly 1 DB call (C hits in-flight re-check), got %d", c)
	}
}

// blockingRecentModelsDB wraps fakeRecentModelsDB, signalling `entered`
// when the DB read starts and blocking on `block` until the test lets it
// finish — so the test can guarantee the cache is empty while caller C
// reads the top level.
type blockingRecentModelsDB struct {
	*fakeRecentModelsDB
	entered chan struct{}
	block   chan struct{}
	once    sync.Once
}

func (f *blockingRecentModelsDB) GetRecentModels(sessionLimit, maxResults int) ([]db.RecentModel, error) {
	f.once.Do(func() { close(f.entered) })
	<-f.block
	return f.fakeRecentModelsDB.GetRecentModels(sessionLimit, maxResults)
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

// blockingSessionDefaultsDB is the session-defaults analogue of
// blockingRecentModelsDB: it signals `entered` when the DB read starts
// and blocks on `block` until released. See installRecheckGate for the
// full ordering that makes the re-check-inside-flight branch
// deterministic.
type blockingSessionDefaultsDB struct {
	*fakeSessionDefaultsDB
	entered chan struct{}
	block   chan struct{}
	once    sync.Once
}

func (f *blockingSessionDefaultsDB) GetSessionDefaults(sessionID, directory string) (db.SessionDefaults, error) {
	f.once.Do(func() { close(f.entered) })
	<-f.block
	return f.fakeSessionDefaultsDB.GetSessionDefaults(sessionID, directory)
}

func TestGetSessionDefaultsCached_RechecksInsideFlight(t *testing.T) {
	resetSessionDefaultsCache()
	t.Cleanup(resetSessionDefaultsCache)
	armGateForC, cParked, releaseC, reset := installRecheckGate(t)
	defer reset()

	blockA := make(chan struct{})
	d := &fakeSessionDefaultsDB{out: db.SessionDefaults{Model: "m"}}
	dGated := &blockingSessionDefaultsDB{
		fakeSessionDefaultsDB: d,
		block:                 blockA,
		entered:               make(chan struct{}),
	}

	aDone := make(chan struct{})
	go func() {
		defer close(aDone)
		if _, err := getSessionDefaultsCached(dGated, "s1", "/repo"); err != nil {
			t.Errorf("caller A: %v", err)
		}
	}()
	<-dGated.entered
	armGateForC()

	cDone := make(chan struct{})
	go func() {
		defer close(cDone)
		got, err := getSessionDefaultsCached(dGated, "s1", "/repo")
		if err != nil {
			t.Errorf("caller C: %v", err)
		}
		if got.Model != "m" {
			t.Errorf("caller C: unexpected result %#v", got)
		}
	}()
	<-cParked

	close(blockA)
	<-aDone
	releaseC()
	<-cDone

	if c := d.calls.Load(); c != 1 {
		t.Fatalf("expected exactly 1 DB call (C hits in-flight re-check), got %d", c)
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
	noSummaryDB
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

type controlledGetSessionsDB struct {
	noSummaryDB
	calls   atomic.Int64
	started chan struct{}

	mu    sync.Mutex
	out   []db.Session
	err   error
	block <-chan struct{}
}

func (f *controlledGetSessionsDB) GetSessions(_ string, _ int64) ([]db.Session, error) {
	f.calls.Add(1)
	f.mu.Lock()
	out, err, block := f.out, f.err, f.block
	f.mu.Unlock()
	if block != nil {
		f.started <- struct{}{}
		<-block
	}
	return out, err
}

func (f *controlledGetSessionsDB) set(out []db.Session, err error, block <-chan struct{}) {
	f.mu.Lock()
	f.out, f.err, f.block = out, err, block
	f.mu.Unlock()
}

// noSummaryDB satisfies the per-session read for fakes whose tests only
// exercise the full-scan path. Reaching it means a test took the
// incremental path without seeding rows for it, so it fails loudly
// instead of silently returning an empty session.
type noSummaryDB struct{}

func (noSummaryDB) GetSessionSummary(sessionID string) (db.Session, error) {
	return db.Session{}, fmt.Errorf("unexpected per-session read for %s", sessionID)
}

// expireSessionsCache makes the snapshot stale without discarding it,
// so the next read takes the stale-while-revalidate path.
func expireSessionsCache() {
	sessionsMu.Lock()
	sessionsExpiresAt = time.Now().Add(-time.Second)
	sessionsMu.Unlock()
}

// drainSessionsRefresh blocks until every background refresh this
// package started has finished. Tests use it instead of sleeping, both
// to assert post-refresh state deterministically and to stop one test's
// refresh goroutine from writing the global snapshot during the next
// test. Callers must first release anything those refreshes block on
// (and cancel any running refresher).
func drainSessionsRefresh() {
	sessionsRefreshWG.Wait()
}

type sessionsResult struct {
	sessions []db.Session
	err      error
}

func readSessionsAsync(d dbGetSessions, directory string) <-chan sessionsResult {
	done := make(chan sessionsResult, 1)
	go func() {
		sessions, err := getSessionsCached(d, directory, 0)
		done <- sessionsResult{sessions: sessions, err: err}
	}()
	return done
}

func resetSessionsCache() {
	// Drain first: a background refresh still running from an earlier
	// test would otherwise repopulate the snapshot we just cleared.
	drainSessionsRefresh()
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

func TestGetSessionsCached_GlobalSnapshotServesExactDirectory(t *testing.T) {
	resetSessionsCache()
	t.Cleanup(resetSessionsCache)

	d := &fakeGetSessionsDB{out: []db.Session{
		{ID: "a-new", Directory: "/a", TimeUpdated: 2000},
		{ID: "b", Directory: "/b", TimeUpdated: 1500},
		{ID: "a-old", Directory: "/a", TimeUpdated: 500},
	}}
	if _, err := getSessionsCached(d, "", 0); err != nil {
		t.Fatalf("warm global snapshot: %v", err)
	}

	got, err := getSessionsCached(d, "/a", 1000)
	if err != nil {
		t.Fatalf("directory read: %v", err)
	}
	if len(got) != 1 || got[0].ID != "a-new" {
		t.Fatalf("directory read = %#v, want exact-directory rows after since filtering", got)
	}
	if calls := d.calls.Load(); calls != 1 {
		t.Fatalf("DB calls = %d, want one global scan", calls)
	}
}

func TestGetSessionsCached_AbsentDirectoryReturnsNonNilEmpty(t *testing.T) {
	resetSessionsCache()
	t.Cleanup(resetSessionsCache)

	d := &fakeGetSessionsDB{out: []db.Session{{ID: "a", Directory: "/a"}}}
	if _, err := getSessionsCached(d, "", 0); err != nil {
		t.Fatalf("warm global snapshot: %v", err)
	}

	got, err := getSessionsCached(d, "/missing", 0)
	if err != nil {
		t.Fatalf("absent directory: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("absent directory = %#v, want non-nil empty slice", got)
	}
	if calls := d.calls.Load(); calls != 1 {
		t.Fatalf("DB calls = %d, want one global scan", calls)
	}
}

func TestGetSessionsCached_StaleReadReturnsWhileBackgroundRefreshRuns(t *testing.T) {
	resetSessionsCache()
	t.Cleanup(resetSessionsCache)

	d := &controlledGetSessionsDB{started: make(chan struct{}, 1), out: []db.Session{{ID: "stale"}}}
	if _, err := getSessionsCached(d, "", 0); err != nil {
		t.Fatalf("warm: %v", err)
	}
	expireSessionsCache()
	release := make(chan struct{})
	d.set([]db.Session{{ID: "fresh"}}, nil, release)

	done := readSessionsAsync(d, "")
	<-d.started
	select {
	case result := <-done:
		if result.err != nil || len(result.sessions) != 1 || result.sessions[0].ID != "stale" {
			t.Fatalf("stale read = %#v, %v", result.sessions, result.err)
		}
		close(release)
	case <-time.After(100 * time.Millisecond):
		close(release)
		<-done
		t.Fatal("stale read waited for refresh")
	}
	// The background refresh must replace the snapshot atomically.
	drainSessionsRefresh()
	got, err := getSessionsCached(d, "", 0)
	if err != nil || len(got) != 1 || got[0].ID != "fresh" {
		t.Fatalf("after refresh = %#v, %v; want the fresh snapshot", got, err)
	}
}

func TestGetSessionsCached_ConcurrentStaleReadsStartOneRefresh(t *testing.T) {
	resetSessionsCache()
	t.Cleanup(resetSessionsCache)

	d := &controlledGetSessionsDB{started: make(chan struct{}, 8), out: []db.Session{{ID: "stale"}}}
	if _, err := getSessionsCached(d, "", 0); err != nil {
		t.Fatalf("warm: %v", err)
	}
	expireSessionsCache()
	release := make(chan struct{})
	d.set([]db.Session{{ID: "fresh"}}, nil, release)

	const readers = 8
	results := make([]<-chan sessionsResult, 0, readers)
	for i := 0; i < readers; i++ {
		results = append(results, readSessionsAsync(d, ""))
	}
	<-d.started
	returned := 0
	deadline := time.After(100 * time.Millisecond)
	for returned < readers {
		select {
		case result := <-results[returned]:
			if result.err != nil || len(result.sessions) != 1 || result.sessions[0].ID != "stale" {
				t.Fatalf("stale read %d = %#v, %v", returned, result.sessions, result.err)
			}
			returned++
		case <-deadline:
			close(release)
			for ; returned < readers; returned++ {
				<-results[returned]
			}
			t.Fatal("concurrent stale reads waited for refresh")
		}
	}
	if calls := d.calls.Load(); calls != 2 {
		t.Fatalf("DB calls while refreshing = %d, want warm plus one refresh", calls)
	}
	close(release)
	drainSessionsRefresh()
}

func TestGetSessionsCached_FailedBackgroundRefreshRetainsStaleAndRetries(t *testing.T) {
	resetSessionsCache()
	t.Cleanup(resetSessionsCache)

	d := &controlledGetSessionsDB{started: make(chan struct{}, 8), out: []db.Session{{ID: "stale"}}}
	if _, err := getSessionsCached(d, "", 0); err != nil {
		t.Fatalf("warm: %v", err)
	}
	expireSessionsCache()
	releaseFailure := make(chan struct{})
	d.set(nil, errors.New("database is locked"), releaseFailure)

	done := readSessionsAsync(d, "")
	<-d.started
	select {
	case result := <-done:
		if result.err != nil || len(result.sessions) != 1 || result.sessions[0].ID != "stale" {
			t.Fatalf("stale read = %#v, %v", result.sessions, result.err)
		}
		close(releaseFailure)
	case <-time.After(100 * time.Millisecond):
		close(releaseFailure)
		<-done
		t.Fatal("stale read waited for failed refresh")
	}

	releaseRetry := make(chan struct{})
	d.set([]db.Session{{ID: "fresh"}}, nil, releaseRetry)
	// Wait for the failed refresh to retire before proving the next read
	// retries rather than being served by a still-running flight.
	drainSessionsRefresh()
	retry := readSessionsAsync(d, "")
	<-d.started
	select {
	case result := <-retry:
		if result.err != nil || len(result.sessions) != 1 || result.sessions[0].ID != "stale" {
			t.Fatalf("retry read = %#v, %v; want retained stale snapshot", result.sessions, result.err)
		}
		close(releaseRetry)
	case <-time.After(100 * time.Millisecond):
		close(releaseRetry)
		<-retry
		t.Fatal("retry read waited for background refresh")
	}
	drainSessionsRefresh()
}

// clearInvalidationFloor moves the last refresh far enough into the
// past that invalidationFloor no longer defers an invalidation, so a
// test can exercise the post-floor behaviour directly.
func clearInvalidationFloor() {
	sessionsMu.Lock()
	lastRefreshCost = time.Millisecond
	lastRefreshEnd = time.Now().Add(-time.Second)
	sessionsMu.Unlock()
}

// Explicit invalidation is not ordinary staleness: it means something
// changed (a session created in another tab, an upstream event), so the
// next read must block on fresh data instead of serving the snapshot
// that is known to be missing it. This fails if invalidation is treated
// as a plain TTL expiry, because then the read returns "old"
// immediately while the fetch is still running.
func TestInvalidateSessionsCache_ForcesSynchronousFreshRead(t *testing.T) {
	resetSessionsCache()
	t.Cleanup(resetSessionsCache)

	d := &controlledGetSessionsDB{started: make(chan struct{}, 1), out: []db.Session{{ID: "old"}}}
	if _, err := getSessionsCached(d, "", 0); err != nil {
		t.Fatalf("warm: %v", err)
	}

	release := make(chan struct{})
	d.set([]db.Session{{ID: "new"}}, nil, release)
	clearInvalidationFloor()
	InvalidateSessionsCache()

	done := readSessionsAsync(d, "")
	<-d.started
	select {
	case result := <-done:
		close(release)
		t.Fatalf("invalidated read returned %#v (%v) before the fetch finished; it must not serve the stale snapshot", result.sessions, result.err)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)

	result := <-done
	if result.err != nil || len(result.sessions) != 1 || result.sessions[0].ID != "new" {
		t.Fatalf("invalidated read = %#v, %v; want the freshly fetched rows", result.sessions, result.err)
	}

	// The successful refresh clears the invalidation, so plain TTL
	// expiry is non-blocking again.
	expireSessionsCache()
	blockNext := make(chan struct{})
	d.set([]db.Session{{ID: "newer"}}, nil, blockNext)
	stale := readSessionsAsync(d, "")
	<-d.started
	select {
	case result := <-stale:
		if result.err != nil || len(result.sessions) != 1 || result.sessions[0].ID != "new" {
			t.Fatalf("post-invalidation TTL read = %#v, %v; want the last good snapshot", result.sessions, result.err)
		}
		close(blockNext)
	case <-time.After(100 * time.Millisecond):
		close(blockNext)
		<-stale
		t.Fatal("TTL expiry blocked: the invalidated state outlived its refresh")
	}
	drainSessionsRefresh()
}

// A forced fetch that fails must not turn a serviceable read into an
// error: the last good snapshot still answers, and the snapshot stays
// invalidated so the next read tries again.
func TestInvalidateSessionsCache_ServesStaleWhenForcedFetchFails(t *testing.T) {
	resetSessionsCache()
	t.Cleanup(resetSessionsCache)

	d := &controlledGetSessionsDB{started: make(chan struct{}, 1), out: []db.Session{{ID: "good"}}}
	if _, err := getSessionsCached(d, "", 0); err != nil {
		t.Fatalf("warm: %v", err)
	}

	d.set(nil, errors.New("database is locked"), nil)
	clearInvalidationFloor()
	InvalidateSessionsCache()

	got, err := getSessionsCached(d, "", 0)
	if err != nil {
		t.Fatalf("expected the stale snapshot, got error: %v", err)
	}
	if len(got) != 1 || got[0].ID != "good" {
		t.Fatalf("forced-fetch failure = %#v, want the retained 'good' rows", got)
	}

	// Still invalidated: the retry must fetch again, not serve stale in
	// the background.
	before := d.calls.Load()
	d.set([]db.Session{{ID: "fresh"}}, nil, nil)
	got, err = getSessionsCached(d, "", 0)
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if len(got) != 1 || got[0].ID != "fresh" {
		t.Fatalf("retry = %#v, want the freshly fetched rows", got)
	}
	if calls := d.calls.Load(); calls != before+1 {
		t.Fatalf("retry DB calls = %d, want exactly one more than %d", calls, before)
	}
}

func TestGetSessionsCached_ColdReadWaitsAndReturnsError(t *testing.T) {
	resetSessionsCache()
	t.Cleanup(resetSessionsCache)

	release := make(chan struct{})
	wantErr := errors.New("cold read failed")
	d := &controlledGetSessionsDB{
		started: make(chan struct{}, 1),
		err:     wantErr,
		block:   release,
	}
	done := readSessionsAsync(d, "")
	<-d.started
	select {
	case <-done:
		close(release)
		t.Fatal("cold read returned before initial refresh completed")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if result := <-done; !errors.Is(result.err, wantErr) {
		t.Fatalf("cold read error = %v, want %v", result.err, wantErr)
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
	drainSessionsRefresh()
	if c := d.calls.Load(); c != 2 {
		t.Errorf("expected 2 DB calls after invalidation, got %d", c)
	}
}

func TestGetSessionsCached_DirectoriesShareGlobalSnapshot(t *testing.T) {
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
	// Every directory and since value filters the same global snapshot.
	if c := d.calls.Load(); c != 1 {
		t.Errorf("expected 1 global DB call, got %d", c)
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
		{ID: "new", Directory: "/repo", TimeUpdated: 2000},
		{ID: "mid", Directory: "/repo", TimeUpdated: 1500},
		{ID: "old", Directory: "/repo", TimeUpdated: 500},
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

	d := &fakeGetSessionsDB{out: []db.Session{{ID: "s", Directory: "/repo"}}}

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

// blockingGetSessionsDB is the sessions analogue of
// blockingRecentModelsDB, used to drive refreshSessions'
// re-check-inside-flight branch deterministically via installRecheckGate.
type blockingGetSessionsDB struct {
	*fakeGetSessionsDB
	entered chan struct{}
	block   chan struct{}
	once    sync.Once
}

func (f *blockingGetSessionsDB) GetSessions(directory string, since int64) ([]db.Session, error) {
	f.once.Do(func() { close(f.entered) })
	<-f.block
	return f.fakeGetSessionsDB.GetSessions(directory, since)
}

func TestRefreshSessions_RechecksInsideFlight(t *testing.T) {
	resetSessionsCache()
	t.Cleanup(resetSessionsCache)
	armGateForC, cParked, releaseC, reset := installRecheckGate(t)
	defer reset()

	blockA := make(chan struct{})
	d := &fakeGetSessionsDB{out: []db.Session{{ID: "s", Directory: "/repo"}}}
	dGated := &blockingGetSessionsDB{
		fakeGetSessionsDB: d,
		block:             blockA,
		entered:           make(chan struct{}),
	}

	aDone := make(chan struct{})
	go func() {
		defer close(aDone)
		if _, err := getSessionsCached(dGated, "/repo", 0); err != nil {
			t.Errorf("caller A: %v", err)
		}
	}()
	<-dGated.entered
	armGateForC()

	cDone := make(chan struct{})
	go func() {
		defer close(cDone)
		got, err := getSessionsCached(dGated, "/repo", 0)
		if err != nil {
			t.Errorf("caller C: %v", err)
		}
		if len(got) != 1 || got[0].ID != "s" {
			t.Errorf("caller C: unexpected result %#v", got)
		}
	}()
	<-cParked

	close(blockA)
	<-aDone
	releaseC()
	<-cDone

	if c := d.calls.Load(); c != 1 {
		t.Fatalf("expected exactly 1 DB call (C hits in-flight re-check), got %d", c)
	}
}

// failableGetSessionsDB serves out/err that the test can flip at
// runtime, so we can warm the cache and then simulate the DB stalling
// out (busy_timeout) on a later refresh.
type failableGetSessionsDB struct {
	noSummaryDB
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

	d := &failableGetSessionsDB{out: []db.Session{{ID: "good", Directory: "/repo"}}}

	// Warm the cache with a good value.
	if _, err := getSessionsCached(d, "/repo", 0); err != nil {
		t.Fatalf("warm: %v", err)
	}

	// Expire the snapshot and make the DB start failing.
	expireSessionsCache()
	d.setErr(errors.New("database is locked"))

	got, err := getSessionsCached(d, "/repo", 0)
	if err != nil {
		t.Fatalf("expected stale value, got error: %v", err)
	}
	if len(got) != 1 || got[0].ID != "good" {
		t.Fatalf("expected stale 'good' value, got %#v", got)
	}
	// The failed background refresh must leave the stale value serving.
	drainSessionsRefresh()
	got, err = getSessionsCached(d, "/repo", 0)
	if err != nil {
		t.Fatalf("after failed refresh, expected stale value, got error: %v", err)
	}
	if len(got) != 1 || got[0].ID != "good" {
		t.Fatalf("after failed refresh: expected stale 'good' value, got %#v", got)
	}
	drainSessionsRefresh()
}

func TestGetSessionsCached_ExpiresAfterTTL(t *testing.T) {
	resetSessionsCache()
	t.Cleanup(resetSessionsCache)

	d := &fakeGetSessionsDB{out: []db.Session{{ID: "s"}}}
	if _, err := getSessionsCached(d, "/repo", 0); err != nil {
		t.Fatalf("first call: %v", err)
	}

	// Force the snapshot to expire.
	expireSessionsCache()

	if _, err := getSessionsCached(d, "/repo", 0); err != nil {
		t.Fatalf("second call: %v", err)
	}
	drainSessionsRefresh()
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
	snapshot, have := sessionsSnapshot, sessionsHave
	sessionsMu.RUnlock()
	if !have || len(snapshot) != 1 || snapshot[0].ID != "s1" {
		t.Fatalf("expected a warm global snapshot, got have=%v snapshot=%#v", have, snapshot)
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

func TestRefreshDelayCapsDutyCycle(t *testing.T) {
	tests := []struct {
		name    string
		elapsed time.Duration
		want    time.Duration
	}{
		{"fast query waits the full interval", 100 * time.Millisecond, sessionsRefreshInterval},
		{"query at the interval waits the interval", sessionsRefreshInterval, sessionsRefreshInterval},
		{"slow query waits as long as it took", 30 * time.Second, 30 * time.Second},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := refreshDelay(tc.elapsed); got != tc.want {
				t.Fatalf("refreshDelay(%v) = %v, want %v", tc.elapsed, got, tc.want)
			}
		})
	}
}

// TestInvalidateSessionsCache_FloorsOnQueryCost pins that invalidation
// can't drive the (multi-second, on a real DB) sessions query faster than
// the query itself takes. Unfloored, every first-seen session ID dropped
// the next read into another full scan, so with several busy OpenCode
// instances the query ran continuously and starved the read pool.
func TestInvalidateSessionsCache_FloorsOnQueryCost(t *testing.T) {
	resetSessionsCache()
	t.Cleanup(resetSessionsCache)

	d := &fakeGetSessionsDB{out: []db.Session{{ID: "s1"}}}
	if _, err := getSessionsCached(d, "", 0); err != nil {
		t.Fatalf("warm: %v", err)
	}
	if c := d.calls.Load(); c != 1 {
		t.Fatalf("warm: calls = %d, want 1", c)
	}

	// Pretend the pass we just made cost a long time. The floor is then
	// well in the future, so invalidation must not force a re-query.
	sessionsMu.Lock()
	lastRefreshCost = time.Hour
	sessionsMu.Unlock()

	for i := 0; i < 5; i++ {
		InvalidateSessionsCache()
		if _, err := getSessionsCached(d, "", 0); err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
	}
	if c := d.calls.Load(); c != 1 {
		t.Errorf("calls = %d, want 1: invalidation bypassed the cost floor", c)
	}

	// Once a query duration has elapsed since the last pass, the same
	// invalidation does force a fresh read.
	sessionsMu.Lock()
	lastRefreshCost = time.Millisecond
	lastRefreshEnd = time.Now().Add(-time.Second)
	sessionsMu.Unlock()

	InvalidateSessionsCache()
	if _, err := getSessionsCached(d, "", 0); err != nil {
		t.Fatalf("post-floor read: %v", err)
	}
	drainSessionsRefresh()
	if c := d.calls.Load(); c != 2 {
		t.Errorf("calls = %d, want 2: invalidation stopped working", c)
	}
}
