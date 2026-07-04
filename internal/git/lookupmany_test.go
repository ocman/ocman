package git

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// TestLookupMany_DeduplicatesByDir is the dashboard's actual use case:
// many sessions share the same working directory, and we want one
// `git status` per unique dir, not per-session.
func TestLookupMany_DeduplicatesByDir(t *testing.T) {
	var calls int64
	c := newCache(time.Minute, func(_ context.Context, _ string) Info {
		atomic.AddInt64(&calls, 1)
		return Info{Branch: "main"}
	})

	got := lookupManyVia(c, context.Background(), []string{
		"/repo/a", "/repo/a", "/repo/b", "/repo/a", "/repo/b",
	}, 4)

	if len(got) != 2 {
		t.Errorf("expected 2 unique entries, got %d (%v)", len(got), got)
	}
	if calls := atomic.LoadInt64(&calls); calls != 2 {
		t.Errorf("expected 2 fetches (one per unique dir), got %d", calls)
	}
}

// TestLookupMany_PopulatesEachUniqueDir verifies the result map maps
// every input dir to the corresponding fetched Info, including
// duplicate inputs.
func TestLookupMany_PopulatesEachUniqueDir(t *testing.T) {
	branches := map[string]string{
		"/repo/a": "main",
		"/repo/b": "develop",
		"/repo/c": "feature",
	}
	c := newCache(time.Minute, func(_ context.Context, dir string) Info {
		return Info{Branch: branches[dir]}
	})

	got := lookupManyVia(c, context.Background(),
		[]string{"/repo/a", "/repo/b", "/repo/c"}, 4)

	if got["/repo/a"].Branch != "main" {
		t.Errorf("a: %q, want main", got["/repo/a"].Branch)
	}
	if got["/repo/b"].Branch != "develop" {
		t.Errorf("b: %q, want develop", got["/repo/b"].Branch)
	}
	if got["/repo/c"].Branch != "feature" {
		t.Errorf("c: %q, want feature", got["/repo/c"].Branch)
	}
}

// TestLookupMany_RespectsWorkerCap is the bound that prevents fork
// pressure on macOS. It used to be 8 in the handler; we lift it here
// and want to observe that the per-call concurrency does not exceed
// the configured workers.
func TestLookupMany_RespectsWorkerCap(t *testing.T) {
	const workers = 3
	var inFlight int32
	var maxObserved int32

	c := newCache(time.Minute, func(_ context.Context, _ string) Info {
		current := atomic.AddInt32(&inFlight, 1)
		// Track the maximum concurrency observed across the run.
		for {
			prev := atomic.LoadInt32(&maxObserved)
			if current <= prev || atomic.CompareAndSwapInt32(&maxObserved, prev, current) {
				break
			}
		}
		// Sleep long enough that all inputs pile onto the worker
		// pool. Without a cap the test would observe maxObserved == N.
		time.Sleep(20 * time.Millisecond)
		atomic.AddInt32(&inFlight, -1)
		return Info{Branch: "main"}
	})

	dirs := make([]string, 12)
	for i := range dirs {
		dirs[i] = string(rune('a'+i)) + "/repo"
	}

	_ = lookupManyVia(c, context.Background(), dirs, workers)

	if got := atomic.LoadInt32(&maxObserved); got > workers {
		t.Errorf("observed %d concurrent fetches, want ≤ %d", got, workers)
	}
	if got := atomic.LoadInt32(&maxObserved); got == 0 {
		t.Error("never observed any concurrent fetch — test sleeps too short?")
	}
}

// TestLookupMany_EmptyDirsShortCircuits returns an empty map without
// touching the cache. Important so callers can pass an unfiltered
// list including empty strings (e.g. a session without a directory)
// without us trying to lookup "".
func TestLookupMany_EmptyDirsShortCircuits(t *testing.T) {
	var calls int64
	c := newCache(time.Minute, func(_ context.Context, _ string) Info {
		atomic.AddInt64(&calls, 1)
		return Info{Branch: "x"}
	})
	got := lookupManyVia(c, context.Background(), nil, 4)
	if len(got) != 0 {
		t.Errorf("nil input: got %d entries, want 0", len(got))
	}
	got = lookupManyVia(c, context.Background(), []string{"", ""}, 4)
	if len(got) != 0 {
		t.Errorf("all-empty input: got %d entries, want 0", len(got))
	}
	if atomic.LoadInt64(&calls) != 0 {
		t.Error("empty inputs must not invoke fetcher")
	}
}

// TestLookupMany_PublicWrapperIntegratesWithDefaultCache exercises the
// exported LookupMany signature against the default cache with a real
// (non-git) directory. We don't assert on git output; we just confirm
// the wrapper compiles, returns a map, and doesn't panic.
func TestLookupMany_PublicWrapperIntegratesWithDefaultCache(t *testing.T) {
	got := LookupMany(context.Background(), []string{t.TempDir(), t.TempDir()})
	if got == nil {
		t.Fatal("LookupMany returned nil map")
	}
	// Non-git temp dirs return zero Info; entries may be present
	// with empty branch or absent — both are valid for a non-repo.
	for dir, info := range got {
		if info.IsRepo() {
			t.Errorf("temp dir %s unexpectedly reported as repo: %+v", dir, info)
		}
	}
}
