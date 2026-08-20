package git

import (
	"context"
	"sync"
)

// defaultLookupManyWorkers caps the number of concurrent `git status`
// invocations. Lifted out of the /api/sessions handler where it lived
// as `gitInfoWorkers = 8`. Each worker runs git in a child process,
// so the bound is a hedge against fork() pressure on the Go runtime —
// every fork briefly stops the world while the address space is
// duplicated, and a burst of unbounded forks was observed to cause
// multi-second pauses across unrelated handlers (see
// docs/other/profiling.md).
//
// The cap is enforced process-wide by fetchSlots in info.go, not just
// per LookupMany call: concurrent /api/git/info requests used to each
// bring their own 8-worker pool, and the combined fork burst blocked
// unrelated handlers (e.g. permission replies).
const defaultLookupManyWorkers = 8

// LookupMany returns Info for every unique non-empty directory in
// dirs, deduplicating along the way. Repeated dirs in the input are
// collapsed; the result is keyed by the unique directory path so a
// caller can iterate session-shaped data and look up each entry's
// info by directory.
//
// The cache TTL applies as for Lookup, so calling LookupMany every
// 30s on the same set of dirs hits the cache after the first call.
//
// Bounded concurrency: at most defaultLookupManyWorkers `git status`
// invocations are in flight simultaneously, regardless of the size
// of dirs. Most calls finish in single-digit ms so larger queues
// drain quickly.
func LookupMany(ctx context.Context, dirs []string) map[string]Info {
	return lookupManyVia(defaultCache, ctx, dirs, defaultLookupManyWorkers)
}

// lookupManyVia is the testable core: takes an explicit cache so unit
// tests can swap fetchFromGit for a deterministic stub.
//
// Workers ≥ 1 enforced; callers passing 0 get a single-threaded scan,
// which is fine but slow for large dir sets — production use goes
// through LookupMany which always passes the configured constant.
func lookupManyVia(c *cache, ctx context.Context, dirs []string, workers int) map[string]Info {
	if len(dirs) == 0 {
		return map[string]Info{}
	}

	// Dedup to unique non-empty directories.
	uniq := make(map[string]struct{}, len(dirs))
	for _, d := range dirs {
		if d != "" {
			uniq[d] = struct{}{}
		}
	}
	if len(uniq) == 0 {
		return map[string]Info{}
	}

	jobs := make(chan string, len(uniq))
	for d := range uniq {
		jobs <- d
	}
	close(jobs)

	if workers < 1 {
		workers = 1
	}
	if workers > len(uniq) {
		workers = len(uniq)
	}

	results := make(map[string]Info, len(uniq))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for dir := range jobs {
				info := c.lookup(ctx, dir)
				mu.Lock()
				results[dir] = info
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return results
}
