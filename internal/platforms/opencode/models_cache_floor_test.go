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
