package opencode

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
)

// Scheduling regressions for the sessions snapshot: ordinary TTL expiry
// must go through the incremental pass (no query when nothing is dirty),
// and the refresher must back off after a failed pass instead of spinning.

func snapshotExpiry() time.Time {
	sessionsMu.RLock()
	defer sessionsMu.RUnlock()
	return sessionsExpiresAt
}

func makeReconcileDue() {
	sessionsMu.Lock()
	lastFullRefresh = time.Now().Add(-2 * sessionsReconcileInterval)
	sessionsMu.Unlock()
}

// shortRetry shrinks the failure/demand cooldown so a test can observe
// several retry windows. Registered before resetSessionsCache's cleanup,
// so it is restored only after the refresher goroutines have drained.
func shortRetry(t *testing.T, d time.Duration) {
	t.Helper()
	prev := sessionsDemandRetry
	sessionsDemandRetry = d
	t.Cleanup(func() { sessionsDemandRetry = prev })
}

func TestGetSessionsCached_CleanTTLExpiryRunsNoQuery(t *testing.T) {
	resetSessionsCache()
	t.Cleanup(resetSessionsCache)

	store := newFakeSessionStore(dirtyFixture...)
	warmSnapshot(t, store)
	expireSessionsCache()

	got, err := getSessionsCached(t.Context(), store, "", 0)
	if err != nil || len(got) != len(dirtyFixture) {
		t.Fatalf("stale read = %#v, %v", got, err)
	}
	drainSessionsRefresh()

	if n := store.fullScans.Load(); n != 1 {
		t.Errorf("full scans = %d, want 1 (clean expiry must not rescan)", n)
	}
	if n := store.summaryReads.Load(); n != 0 {
		t.Errorf("summary reads = %d, want 0", n)
	}
	if !time.Now().Before(snapshotExpiry()) {
		t.Error("clean expiry did not renew snapshot freshness")
	}
}

func TestGetSessionsCached_ExpiryRunsDueReconciliationOnce(t *testing.T) {
	resetSessionsCache()
	t.Cleanup(resetSessionsCache)

	store := newFakeSessionStore(dirtyFixture...)
	warmSnapshot(t, store)
	store.put(db.Session{ID: "s-silent", Title: "Silent", Directory: "/c", TimeUpdated: 4000})
	expireSessionsCache()
	makeReconcileDue()

	for i := 0; i < 3; i++ {
		if _, err := getSessionsCached(t.Context(), store, "", 0); err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		drainSessionsRefresh()
	}
	if n := store.fullScans.Load(); n != 2 {
		t.Errorf("full scans = %d, want warm + exactly one reconciliation", n)
	}
	if got, want := currentSnapshot(), mustFullScan(t, store); !reflect.DeepEqual(got, want) {
		t.Errorf("reconciled snapshot\n got = %#v\nwant = %#v", got, want)
	}
}

func TestGetSessionsCached_DirtyExpiryRecomputesOnlyDirtyRows(t *testing.T) {
	resetSessionsCache()
	t.Cleanup(resetSessionsCache)

	store := newFakeSessionStore(dirtyFixture...)
	warmSnapshot(t, store)
	store.put(db.Session{ID: "s-mid", Title: "Mid (renamed)", Directory: "/b", TimeUpdated: 5000})
	MarkSessionDirty("s-mid")
	expireSessionsCache()

	if _, err := getSessionsCached(t.Context(), store, "", 0); err != nil {
		t.Fatalf("stale read: %v", err)
	}
	drainSessionsRefresh()

	if n := store.fullScans.Load(); n != 1 {
		t.Errorf("full scans = %d, want 1", n)
	}
	if ids := store.recordedSummaryIDs(); !reflect.DeepEqual(ids, []string{"s-mid"}) {
		t.Errorf("summary reads = %v, want [s-mid]", ids)
	}
	if got, want := currentSnapshot(), mustFullScan(t, store); !reflect.DeepEqual(got, want) {
		t.Errorf("snapshot\n got = %#v\nwant = %#v", got, want)
	}
}

// refresherAttempts runs the refresher against a failing store for a
// window spanning a few cooldowns and returns how many full scans it
// attempted, then proves cancellation stops the goroutine promptly.
func refresherAttempts(t *testing.T, store *fakeSessionStore, during func()) int64 {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	StartSessionsRefresher(ctx, store, nil)
	deadline := time.Now().Add(120 * time.Millisecond)
	for time.Now().Before(deadline) {
		if during != nil {
			during()
		}
		time.Sleep(time.Millisecond)
	}
	attempts := store.fullScans.Load()
	cancel()
	stopped := make(chan struct{})
	go func() { drainSessionsRefresh(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("refresher did not stop promptly after cancel")
	}
	return attempts
}

// 120ms window / 40ms cooldown: the first attempt plus about three
// retries. Without a cooldown a fast-failing scan retries thousands of
// times in the same window.
const maxFailingAttempts = 6

func TestStartSessionsRefresher_ColdFailureBacksOff(t *testing.T) {
	shortRetry(t, 40*time.Millisecond)
	resetSessionsCache()
	t.Cleanup(resetSessionsCache)

	store := newFakeSessionStore(dirtyFixture...)
	store.failFullScans(errors.New("database is locked"))

	if n := refresherAttempts(t, store, nil); n < 1 || n > maxFailingAttempts {
		t.Fatalf("full-scan attempts = %d, want 1..%d", n, maxFailingAttempts)
	}
}

func TestStartSessionsRefresher_OverdueWarmFailureBacksOffUnderEvents(t *testing.T) {
	shortRetry(t, 40*time.Millisecond)
	resetSessionsCache()
	t.Cleanup(resetSessionsCache)

	store := newFakeSessionStore(dirtyFixture...)
	warmSnapshot(t, store)
	makeReconcileDue()
	store.failFullScans(errors.New("database is locked"))
	store.fullScans.Store(0)

	// Events keep arriving while the scan fails; they must not bypass
	// the cooldown either.
	n := refresherAttempts(t, store, func() { MarkSessionDirty("s-mid") })
	if n < 1 || n > maxFailingAttempts {
		t.Fatalf("full-scan attempts = %d, want 1..%d", n, maxFailingAttempts)
	}
	if got := dirtyIDs(); !reflect.DeepEqual(got, []string{"s-mid"}) {
		t.Errorf("dirty ids after failures = %v, want [s-mid] kept", got)
	}
}

func TestStartSessionsRefresher_RecoveryProcessesPendingWork(t *testing.T) {
	shortRetry(t, 20*time.Millisecond)
	resetSessionsCache()
	t.Cleanup(resetSessionsCache)

	store := newFakeSessionStore(dirtyFixture...)
	warmSnapshot(t, store)
	makeReconcileDue()
	store.failFullScans(errors.New("database is locked"))

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); drainSessionsRefresh() })
	StartSessionsRefresher(ctx, store, nil)
	waitFor(t, time.Second, func() bool { return store.fullScans.Load() >= 2 })

	store.put(db.Session{ID: "s-mid", Title: "Mid (renamed)", Directory: "/b", TimeUpdated: 5000})
	MarkSessionDirty("s-mid")
	store.failFullScans(nil)

	want := mustFullScan(t, store)
	waitFor(t, time.Second, func() bool {
		return reflect.DeepEqual(currentSnapshot(), want) && len(dirtyIDs()) == 0
	})
}
