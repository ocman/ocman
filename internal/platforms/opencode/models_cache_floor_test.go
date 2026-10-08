package opencode

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"testing/synctest"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
)

func TestSessionsStreamingRefreshFloor(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		resetSessionsCache()
		defer resetSessionsCache()
		wake := sessionsRefreshWake
		sessionsRefreshWake = make(chan struct{}, 1)
		defer func() { sessionsRefreshWake = wake }()
		store := newFakeSessionStore(dirtyFixture...)
		ctx, cancel := context.WithCancel(t.Context())
		defer func() { cancel(); drainSessionsRefresh() }()
		StartSessionsRefresher(ctx, store, nil)
		synctest.Wait()
		MarkSessionDirty("s-mid")
		synctest.Wait()
		for i := 0; i < 100; i++ {
			store.put(db.Session{ID: "s-mid", Title: "latest", TimeUpdated: int64(5000 + i)})
			MarkSessionDirty("s-mid")
			// Poll-triggered refreshes must share the same floor.
			if _, err := getSessionsCached(ctx, store, "", 0); err != nil {
				t.Fatal(err)
			}
			time.Sleep(5 * time.Millisecond)
			synctest.Wait()
		}
		if got := store.summaryReads.Load(); got != 1 {
			t.Fatalf("summary reads during burst = %d, want 1", got)
		}
		store.put(db.Session{ID: "s-old", Title: "also changed", TimeUpdated: 6000})
		MarkSessionDirty("s-old")
		synctest.Wait()
		if got := dirtyIDs(); !reflect.DeepEqual(got, []string{"s-mid", "s-old"}) {
			t.Fatalf("pending rows = %v", got)
		}
		// No further event is needed to drain all queued rows at the floor.
		time.Sleep(500 * time.Millisecond)
		synctest.Wait()
		if got := store.summaryReads.Load(); got != 3 {
			t.Fatalf("summary reads after floor = %d, want 3", got)
		}
		if got := currentSnapshot(); !reflect.DeepEqual(got, store.listRows()) || len(dirtyIDs()) != 0 {
			t.Fatalf("queued rows were lost: %#v", got)
		}
		MarkSessionDirty("s-mid")
		synctest.Wait()
		cancel()
		drainSessionsRefresh()
		before := store.summaryReads.Load()
		time.Sleep(2 * time.Second)
		synctest.Wait()
		if got := store.summaryReads.Load(); got != before {
			t.Fatalf("refresh continued after cancellation: %d -> %d", before, got)
		}
	})
}

func TestSessionsRefreshFloorPreservesFailureBackoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		resetSessionsCache()
		defer resetSessionsCache()
		wake := sessionsRefreshWake
		sessionsRefreshWake = make(chan struct{}, 1)
		defer func() { sessionsRefreshWake = wake }()
		previousInterval := sessionsIncrementalInterval
		sessionsIncrementalInterval = 100 * time.Millisecond
		defer func() { sessionsIncrementalInterval = previousInterval }()
		store := newFakeSessionStore(dirtyFixture...)
		ctx, cancel := context.WithCancel(t.Context())
		defer func() { cancel(); drainSessionsRefresh() }()
		StartSessionsRefresher(ctx, store, nil)
		MarkSessionDirty("s-mid")
		synctest.Wait()
		// The startup full scan may have claimed the first mark.
		MarkSessionDirty("s-mid")
		synctest.Wait()
		before := store.summaryReads.Load()
		store.failSummaries(errors.New("database is locked"))
		MarkSessionDirty("s-mid")
		time.Sleep(sessionsIncrementalInterval)
		synctest.Wait()
		if got := store.summaryReads.Load(); got != before+1 {
			t.Fatalf("first failing attempt = %d, want %d", got, before+1)
		}
		for i := 0; i < 100; i++ {
			MarkSessionDirty("s-mid")
			time.Sleep(10 * time.Millisecond)
			synctest.Wait()
		}
		if got := store.summaryReads.Load(); got != before+1 {
			t.Fatalf("events bypassed failure backoff: %d", got)
		}
		store.failSummaries(nil)
		time.Sleep(sessionsDemandRetry - time.Second)
		synctest.Wait()
		if got := store.summaryReads.Load(); got != before+2 || len(dirtyIDs()) != 0 {
			t.Fatalf("failure recovery lost pending work: reads=%d dirty=%v", got, dirtyIDs())
		}
	})
}

func TestSessionsRefreshFloorUrgentPaths(t *testing.T) {
	for _, path := range []string{"new session", "invalidation", "full dirty", "reconciliation", "cold"} {
		t.Run(path, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				resetSessionsCache()
				defer resetSessionsCache()
				store := newFakeSessionStore(dirtyFixture...)
				warmSnapshot(t, store)
				MarkSessionDirty("s-mid")
				if _, err := refreshSessionsIncremental(t.Context(), store); err != nil {
					t.Fatal(err)
				}
				store.put(db.Session{ID: "added", TimeUpdated: 9000})
				time.Sleep(time.Nanosecond)
				started := time.Now()
				switch path {
				case "new session":
					if err := refreshSessionNow(t.Context(), store, "added"); err != nil {
						t.Fatal(err)
					}
				case "invalidation":
					InvalidateSessionsCache()
					if _, err := getSessionsCached(t.Context(), store, "", 0); err != nil {
						t.Fatal(err)
					}
				default:
					switch path {
					case "full dirty":
						MarkSessionsDirty()
					case "reconciliation":
						makeReconcileDue()
					case "cold":
						sessionsMu.Lock()
						sessionsHave = false
						sessionsMu.Unlock()
					}
					if _, err := refreshSessionsIncremental(t.Context(), store); err != nil {
						t.Fatal(err)
					}
				}
				if elapsed := time.Since(started); elapsed != 0 {
					t.Fatalf("urgent path waited %v", elapsed)
				}
				if got := currentSnapshot(); !reflect.DeepEqual(got, store.listRows()) {
					t.Fatalf("urgent path did not publish new row: %#v", got)
				}
			})
		})
	}
}

type slowFloorStore struct {
	*fakeSessionStore
	fullDelay, summaryDelay time.Duration
}

func (s *slowFloorStore) leavePending(delay time.Duration) {
	if delay == 0 {
		return
	}
	time.Sleep(delay)
	MarkSessionDirty("s-mid")
	// Exercise the timer without an event wake masking its deadline.
	<-sessionsRefreshWake
}

func (s *slowFloorStore) GetSessions(ctx context.Context, dir string, since int64) ([]db.Session, error) {
	out, err := s.fakeSessionStore.GetSessions(ctx, dir, since)
	s.leavePending(s.fullDelay)
	return out, err
}

func (s *slowFloorStore) GetSessionSummary(ctx context.Context, id string) (db.Session, error) {
	out, err := s.fakeSessionStore.GetSessionSummary(ctx, id)
	if s.summaryReads.Load() == 1 {
		s.leavePending(s.summaryDelay)
	}
	return out, err
}

func TestSessionsRefreshFloorPreservesSlowPassBudget(t *testing.T) {
	for _, full := range []bool{true, false} {
		t.Run(map[bool]string{true: "full", false: "incremental"}[full], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				resetSessionsCache()
				defer resetSessionsCache()
				wake := sessionsRefreshWake
				sessionsRefreshWake = make(chan struct{}, 1)
				defer func() { sessionsRefreshWake = wake }()
				store := &slowFloorStore{fakeSessionStore: newFakeSessionStore(dirtyFixture...)}
				if full {
					store.fullDelay = 3 * time.Second
				} else {
					store.summaryDelay = 3 * time.Second
				}
				ctx, cancel := context.WithCancel(t.Context())
				defer func() { cancel(); drainSessionsRefresh() }()
				StartSessionsRefresher(ctx, store, nil)
				if !full {
					synctest.Wait()
					MarkSessionDirty("s-mid")
				}
				time.Sleep(3 * time.Second)
				synctest.Wait()
				want := int64(1)
				if full {
					want = 0
				}
				time.Sleep(time.Second)
				synctest.Wait()
				if got := store.summaryReads.Load(); got != want {
					t.Fatalf("timer bypassed slow-pass budget: summary reads=%d, want %d", got, want)
				}
				time.Sleep(2 * time.Second)
				synctest.Wait()
				if got := store.summaryReads.Load(); got != want+1 || len(dirtyIDs()) != 0 {
					t.Fatalf("budget expiry lost pending work: reads=%d dirty=%v", got, dirtyIDs())
				}
			})
		})
	}
}
