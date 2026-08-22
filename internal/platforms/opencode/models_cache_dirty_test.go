package opencode

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
)

// fakeSessionStore stands in for the OpenCode database. Both reads are
// derived from the same rows, so a merged snapshot can be compared
// against a clean full scan of the same data — the equivalence property
// incremental refresh has to hold.
//
// GetSessions mirrors db.GetSessions: ordered by time_updated DESC with
// parentless subagents dropped after the query. GetSessionSummary
// mirrors db.GetSessionSummary: one row, or db.ErrSessionNotFound when
// the list would not contain it.
type fakeSessionStore struct {
	mu   sync.Mutex
	rows map[string]db.Session

	fullScans    atomic.Int64
	summaryReads atomic.Int64

	readMu     sync.Mutex
	summaryIDs []string
	summaryErr error
}

func newFakeSessionStore(rows ...db.Session) *fakeSessionStore {
	f := &fakeSessionStore{rows: map[string]db.Session{}}
	for _, r := range rows {
		f.rows[r.ID] = r
	}
	return f
}

func (f *fakeSessionStore) put(s db.Session) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows[s.ID] = s
}

func (f *fakeSessionStore) remove(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.rows, id)
}

// listed reports whether the row survives the post-query filter that
// db.GetSessions applies in Go.
func listed(s db.Session) bool {
	return s.ParentID != "" || !strings.HasSuffix(s.Title, " subagent)")
}

func (f *fakeSessionStore) GetSessions(context.Context, string, int64) ([]db.Session, error) {
	f.fullScans.Add(1)
	return f.listRows(), nil
}

// listRows is GetSessions without the call accounting, so a test can
// compute the reference full-scan result without inflating the scan
// count it is asserting on.
func (f *fakeSessionStore) listRows() []db.Session {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]db.Session, 0, len(f.rows))
	for _, s := range f.rows {
		if listed(s) {
			out = append(out, s)
		}
	}
	// time_updated DESC, ties broken by id so the fixture is
	// deterministic (SQLite's tie order is unspecified).
	sort.Slice(out, func(i, j int) bool {
		if out[i].TimeUpdated != out[j].TimeUpdated {
			return out[i].TimeUpdated > out[j].TimeUpdated
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func (f *fakeSessionStore) GetSessionSummary(_ context.Context, sessionID string) (db.Session, error) {
	f.summaryReads.Add(1)
	f.readMu.Lock()
	f.summaryIDs = append(f.summaryIDs, sessionID)
	err := f.summaryErr
	f.readMu.Unlock()
	if err != nil {
		return db.Session{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.rows[sessionID]
	if !ok || !listed(s) {
		return db.Session{}, db.ErrSessionNotFound
	}
	return s, nil
}

func (f *fakeSessionStore) recordedSummaryIDs() []string {
	f.readMu.Lock()
	defer f.readMu.Unlock()
	return append([]string(nil), f.summaryIDs...)
}

func (f *fakeSessionStore) failSummaries(err error) {
	f.readMu.Lock()
	defer f.readMu.Unlock()
	f.summaryErr = err
}

// currentSnapshot copies the cached snapshot under the read lock.
func currentSnapshot() []db.Session {
	sessionsMu.RLock()
	defer sessionsMu.RUnlock()
	return append([]db.Session(nil), sessionsSnapshot...)
}

func dirtyIDs() []string {
	sessionsMu.RLock()
	defer sessionsMu.RUnlock()
	out := make([]string, 0, len(sessionsDirty))
	for id := range sessionsDirty {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// warmSnapshot loads the store into the cache with one full scan.
func warmSnapshot(t *testing.T, store *fakeSessionStore) {
	t.Helper()
	if _, err := getSessionsCached(t.Context(), store, "", 0); err != nil {
		t.Fatalf("warm snapshot: %v", err)
	}
}

func mustFullScan(t *testing.T, store *fakeSessionStore) []db.Session {
	t.Helper()
	return store.listRows()
}

var _ dbGetSessions = (*fakeSessionStore)(nil)

var dirtyFixture = []db.Session{
	{ID: "s-new", Title: "New", Directory: "/a", TimeUpdated: 3000},
	{ID: "s-mid", Title: "Mid", Directory: "/b", TimeUpdated: 2000},
	{ID: "s-old", Title: "Old", Directory: "/a", TimeUpdated: 1000},
}

// TestRefreshSessionsIncremental_UpdatedSessionMatchesFullScan is the
// equivalence gate: recomputing one changed session and merging it must
// leave exactly the snapshot a clean full scan produces, including the
// time_updated DESC ordering after the row moves.
func TestRefreshSessionsIncremental_UpdatedSessionMatchesFullScan(t *testing.T) {
	resetSessionsCache()
	t.Cleanup(resetSessionsCache)

	store := newFakeSessionStore(dirtyFixture...)
	warmSnapshot(t, store)

	// One session changes in isolation: new title, new tokens, and a
	// timestamp that moves it to the front of the list.
	store.put(db.Session{ID: "s-old", Title: "Old (renamed)", Directory: "/a", TimeUpdated: 9000, TotalInputTokens: 42})
	MarkSessionDirty("s-old")

	if _, err := refreshSessionsIncremental(t.Context(), store); err != nil {
		t.Fatalf("incremental refresh: %v", err)
	}

	want := mustFullScan(t, store)
	if got := currentSnapshot(); !reflect.DeepEqual(got, want) {
		t.Errorf("merged snapshot\n got = %#v\nwant = %#v", got, want)
	}
}

// TestRefreshSessionsIncremental_LeavesUnchangedSessionsAlone pins the
// point of the exercise: only the dirty session is recomputed, and no
// full aggregate scan runs.
func TestRefreshSessionsIncremental_LeavesUnchangedSessionsAlone(t *testing.T) {
	resetSessionsCache()
	t.Cleanup(resetSessionsCache)

	store := newFakeSessionStore(dirtyFixture...)
	warmSnapshot(t, store)
	scansAfterWarm := store.fullScans.Load()

	MarkSessionDirty("s-mid")
	if _, err := refreshSessionsIncremental(t.Context(), store); err != nil {
		t.Fatalf("incremental refresh: %v", err)
	}

	if got := store.fullScans.Load(); got != scansAfterWarm {
		t.Errorf("full scans = %d, want %d (no rescan for one dirty session)", got, scansAfterWarm)
	}
	if got := store.recordedSummaryIDs(); !reflect.DeepEqual(got, []string{"s-mid"}) {
		t.Errorf("recomputed sessions = %v, want only [s-mid]", got)
	}
}

// TestRefreshSessionsIncremental_DeletedSessionIsDropped covers the
// case the single-session read reports as missing: the row leaves the
// snapshot rather than lingering with stale values.
func TestRefreshSessionsIncremental_DeletedSessionIsDropped(t *testing.T) {
	resetSessionsCache()
	t.Cleanup(resetSessionsCache)

	store := newFakeSessionStore(dirtyFixture...)
	warmSnapshot(t, store)

	store.remove("s-mid")
	MarkSessionDirty("s-mid")
	if _, err := refreshSessionsIncremental(t.Context(), store); err != nil {
		t.Fatalf("incremental refresh: %v", err)
	}

	want := mustFullScan(t, store)
	got := currentSnapshot()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot after deletion\n got = %#v\nwant = %#v", got, want)
	}
	for _, s := range got {
		if s.ID == "s-mid" {
			t.Fatalf("deleted session still present: %#v", got)
		}
	}
	if store.fullScans.Load() != 1 {
		t.Errorf("full scans = %d, want 1 (deletion handled incrementally)", store.fullScans.Load())
	}
}

// TestRefreshSessionsIncremental_ParentlessSubagentStaysFiltered pins
// that the post-query filter GetSessions applies is preserved by the
// merge: a session the full scan never lists must not appear just
// because an event marked it dirty.
func TestRefreshSessionsIncremental_ParentlessSubagentStaysFiltered(t *testing.T) {
	resetSessionsCache()
	t.Cleanup(resetSessionsCache)

	store := newFakeSessionStore(dirtyFixture...)
	warmSnapshot(t, store)

	store.put(db.Session{ID: "s-judge", Title: "Judge (auto-approve subagent)", Directory: "/a", TimeUpdated: 9999})
	MarkSessionDirty("s-judge")
	if _, err := refreshSessionsIncremental(t.Context(), store); err != nil {
		t.Fatalf("incremental refresh: %v", err)
	}

	want := mustFullScan(t, store)
	if got := currentSnapshot(); !reflect.DeepEqual(got, want) {
		t.Errorf("snapshot\n got = %#v\nwant = %#v (parentless subagent must stay filtered)", got, want)
	}
}

// TestRefreshSessionsIncremental_NoDirtySessionsRunsNoQuery is the
// saving itself: an idle refresher tick touches the database not at all.
func TestRefreshSessionsIncremental_NoDirtySessionsRunsNoQuery(t *testing.T) {
	resetSessionsCache()
	t.Cleanup(resetSessionsCache)

	store := newFakeSessionStore(dirtyFixture...)
	warmSnapshot(t, store)
	expireSessionsCache()

	if _, err := refreshSessionsIncremental(t.Context(), store); err != nil {
		t.Fatalf("incremental refresh: %v", err)
	}

	if got := store.fullScans.Load(); got != 1 {
		t.Errorf("full scans = %d, want 1 (idle tick must not rescan)", got)
	}
	if got := store.summaryReads.Load(); got != 0 {
		t.Errorf("per-session reads = %d, want 0", got)
	}
	sessionsMu.RLock()
	expiresAt := sessionsExpiresAt
	sessionsMu.RUnlock()
	if !time.Now().Before(expiresAt) {
		t.Error("an up-to-date snapshot must have its freshness extended, or reads fall back to full scans")
	}
}

// TestRefreshSessionsIncremental_ReconcilesOnSchedule pins that
// correctness never rests on events alone: a change nothing marked
// dirty is still corrected by the periodic full reconciliation.
func TestRefreshSessionsIncremental_ReconcilesOnSchedule(t *testing.T) {
	resetSessionsCache()
	t.Cleanup(resetSessionsCache)

	store := newFakeSessionStore(dirtyFixture...)
	warmSnapshot(t, store)

	// A change that emitted no event at all.
	store.put(db.Session{ID: "s-silent", Title: "Silent", Directory: "/c", TimeUpdated: 4000})

	// Not due yet: the snapshot stays as it was.
	if _, err := refreshSessionsIncremental(t.Context(), store); err != nil {
		t.Fatalf("incremental refresh: %v", err)
	}
	if len(currentSnapshot()) != len(dirtyFixture) {
		t.Fatalf("silent change appeared before reconciliation was due: %#v", currentSnapshot())
	}

	// Due: the full scan runs and picks it up.
	sessionsMu.Lock()
	lastFullRefresh = time.Now().Add(-2 * sessionsReconcileInterval)
	sessionsMu.Unlock()

	if _, err := refreshSessionsIncremental(t.Context(), store); err != nil {
		t.Fatalf("reconciling refresh: %v", err)
	}
	want := mustFullScan(t, store)
	if got := currentSnapshot(); !reflect.DeepEqual(got, want) {
		t.Errorf("reconciled snapshot\n got = %#v\nwant = %#v", got, want)
	}
	if got := store.fullScans.Load(); got < 2 {
		t.Errorf("full scans = %d, want the reconciliation scan to have run", got)
	}
}

// TestMarkSessionsDirty_ForcesFullReconciliation covers the fail-safe
// for events that cannot be attributed to a session.
func TestMarkSessionsDirty_ForcesFullReconciliation(t *testing.T) {
	resetSessionsCache()
	t.Cleanup(resetSessionsCache)

	store := newFakeSessionStore(dirtyFixture...)
	warmSnapshot(t, store)

	store.put(db.Session{ID: "s-unattributed", Title: "Unattributed", Directory: "/c", TimeUpdated: 4000})
	MarkSessionsDirty()

	if _, err := refreshSessionsIncremental(t.Context(), store); err != nil {
		t.Fatalf("incremental refresh: %v", err)
	}
	want := mustFullScan(t, store)
	if got := currentSnapshot(); !reflect.DeepEqual(got, want) {
		t.Errorf("snapshot\n got = %#v\nwant = %#v", got, want)
	}
}

// TestRefreshSessionsIncremental_FailureKeepsSnapshotAndDirty pins that
// a failed per-session read neither evicts the last good snapshot nor
// loses the dirty indication.
func TestRefreshSessionsIncremental_FailureKeepsSnapshotAndDirty(t *testing.T) {
	resetSessionsCache()
	t.Cleanup(resetSessionsCache)

	store := newFakeSessionStore(dirtyFixture...)
	warmSnapshot(t, store)
	before := currentSnapshot()

	store.failSummaries(errors.New("database is locked"))
	MarkSessionDirty("s-mid")
	if _, err := refreshSessionsIncremental(t.Context(), store); err == nil {
		t.Fatal("expected the per-session read failure to surface")
	}

	if got := currentSnapshot(); !reflect.DeepEqual(got, before) {
		t.Errorf("snapshot after failed refresh = %#v, want the last good %#v", got, before)
	}
	if got := dirtyIDs(); !reflect.DeepEqual(got, []string{"s-mid"}) {
		t.Errorf("dirty ids after failure = %v, want [s-mid] retained for retry", got)
	}
}

// TestRefreshSessionsIncremental_DoesNotSwallowInvalidation pins that
// an incremental pass neither escalates an explicit invalidation into an
// immediate full scan (which would bypass its rate-limiting floor) nor
// pushes the snapshot's expiry back out past it (which would stop the
// next read blocking on fresh data, as invalidation promises).
func TestRefreshSessionsIncremental_DoesNotSwallowInvalidation(t *testing.T) {
	resetSessionsCache()
	t.Cleanup(resetSessionsCache)

	store := newFakeSessionStore(dirtyFixture...)
	warmSnapshot(t, store)

	// Put the invalidation floor definitively in the past. It defers an
	// invalidation until one query duration after the last scan, and
	// against a fake database that window is sub-microsecond — short
	// enough that whether it has elapsed is a coin flip.
	sessionsMu.Lock()
	lastRefreshEnd = time.Now().Add(-time.Minute)
	lastRefreshCost = 0
	sessionsMu.Unlock()

	InvalidateSessionsCache()
	MarkSessionDirty("s-mid")
	store.put(db.Session{ID: "s-mid", Title: "Mid (renamed)", Directory: "/b", TimeUpdated: 2500})

	if _, err := refreshSessionsIncremental(t.Context(), store); err != nil {
		t.Fatalf("incremental refresh: %v", err)
	}
	if got := store.fullScans.Load(); got != 1 {
		t.Errorf("full scans = %d, want 1 (incremental must not run the invalidation's scan)", got)
	}

	// The invalidation is still pending, so the next read fetches.
	if _, err := getSessionsCached(t.Context(), store, "", 0); err != nil {
		t.Fatalf("read after invalidation: %v", err)
	}
	if got := store.fullScans.Load(); got != 2 {
		t.Errorf("full scans = %d, want 2 (invalidation still forces a synchronous fetch)", got)
	}
}

// TestGetSessionsCached_FreshDirtyReadRefreshesInBackground pins that a
// dirty session is picked up without the read blocking on it.
func TestGetSessionsCached_FreshDirtyReadRefreshesInBackground(t *testing.T) {
	resetSessionsCache()
	t.Cleanup(resetSessionsCache)

	store := newFakeSessionStore(dirtyFixture...)
	warmSnapshot(t, store)

	store.put(db.Session{ID: "s-mid", Title: "Mid (renamed)", Directory: "/b", TimeUpdated: 2500})
	MarkSessionDirty("s-mid")

	if _, err := getSessionsCached(t.Context(), store, "", 0); err != nil {
		t.Fatalf("dirty read: %v", err)
	}
	drainSessionsRefresh()

	want := mustFullScan(t, store)
	if got := currentSnapshot(); !reflect.DeepEqual(got, want) {
		t.Errorf("snapshot after background dirty refresh\n got = %#v\nwant = %#v", got, want)
	}
	if got := store.fullScans.Load(); got != 1 {
		t.Errorf("full scans = %d, want 1", got)
	}
}

type cancelableSerializedRefreshDB struct {
	firstEntered chan struct{}
	calls        atomic.Int64
	active       atomic.Int64
	maxActive    atomic.Int64
}

func (d *cancelableSerializedRefreshDB) GetSessions(ctx context.Context, _ string, _ int64) ([]db.Session, error) {
	call := d.calls.Add(1)
	active := d.active.Add(1)
	defer d.active.Add(-1)
	for max := d.maxActive.Load(); active > max && !d.maxActive.CompareAndSwap(max, active); max = d.maxActive.Load() {
	}
	if call == 1 {
		close(d.firstEntered)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return []db.Session{{ID: "newer", TimeUpdated: 2}}, nil
}

func (*cancelableSerializedRefreshDB) GetSessionSummary(context.Context, string) (db.Session, error) {
	return db.Session{}, db.ErrSessionNotFound
}

func TestRefreshSessionsForRequest_SerializesCanceledLeaderAndIncrementalFollower(t *testing.T) {
	resetSessionsCache()
	t.Cleanup(resetSessionsCache)

	d := &cancelableSerializedRefreshDB{firstEntered: make(chan struct{})}
	joined := make(chan struct{}, 3)
	afterSessionsFlightJoin = func() { joined <- struct{}{} }
	t.Cleanup(func() { afterSessionsFlightJoin = nil })
	requestCtx, cancelRequest := context.WithCancel(t.Context())
	requestDone := make(chan error, 1)
	go func() {
		_, err := refreshSessionsForRequest(requestCtx, d)
		requestDone <- err
	}()
	<-d.firstEntered
	<-joined

	incrementalDone := make(chan error, 1)
	go func() {
		_, err := refreshSessionsIncremental(t.Context(), d)
		incrementalDone <- err
	}()
	select {
	case <-joined:
	case <-time.After(time.Second):
		t.Fatal("incremental refresh did not enter sessions flight")
	}

	cancelRequest()
	if err := <-requestDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("request error = %v, want context.Canceled", err)
	}
	if err := <-incrementalDone; err != nil {
		t.Fatalf("incremental refresh: %v", err)
	}
	if got := d.maxActive.Load(); got != 1 {
		t.Fatalf("concurrent DB refreshes = %d, want 1", got)
	}
	if got := currentSnapshot(); !reflect.DeepEqual(got, []db.Session{{ID: "newer", TimeUpdated: 2}}) {
		t.Fatalf("snapshot = %#v, want newer incremental result", got)
	}
}
