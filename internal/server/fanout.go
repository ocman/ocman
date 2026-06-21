package server

import (
	"context"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

// remoteFanoutTimeout bounds how long a single remote adapter may take to
// return its session list before it is dropped from the aggregate. The
// unified list must never block on one slow/offline remote (FR-15,
// NFR-1). The local adapter is not subject to this timeout — it is fast
// and in-process. Tunable per OQ-5; 2s is the documented default.
const remoteFanoutTimeout = 2 * time.Second

// isLocalAdapter reports whether an adapter is the in-process local one
// (a bare platform id with no "r-...:" prefix). Local adapters keep their
// current latency; remote adapters get the fan-out timeout.
func isLocalAdapter(p platforms.Platform) bool {
	id := string(p.ID())
	return len(id) < 2 || id[:2] != "r-"
}

// fanOutSessions lists sessions across every available adapter
// concurrently, merging whatever returns. Remote adapters are bounded by
// remoteFanoutTimeout so a slow/offline remote never delays the result;
// they return last-known stale rows on failure (handled inside the
// remotePlatform adapter). remember is called for each adapter's result
// (may be nil) so the registry's reverse-lookup cache stays warm.
func (s *Server) fanOutSessions(ctx context.Context, dir string, since int64, remember func(platforms.ID, []db.Session)) []db.Session {
	adapters := s.registry.Platforms()

	type result struct {
		id       platforms.ID
		sessions []db.Session
	}
	results := make([]result, len(adapters))

	var wg sync.WaitGroup
	for i, adapter := range adapters {
		if !adapter.Available(ctx) {
			continue
		}
		wg.Add(1)
		go func(i int, adapter platforms.Platform) {
			defer wg.Done()

			callCtx := ctx
			if !isLocalAdapter(adapter) {
				var cancel context.CancelFunc
				callCtx, cancel = context.WithTimeout(ctx, remoteFanoutTimeout)
				defer cancel()
			}

			sessions, err := adapter.Sessions(callCtx, dir, since)
			if err != nil {
				log.WithFields(log.Fields{"platform": adapter.ID(), "error": err}).
					Warn("listing sessions from platform")
				return
			}
			results[i] = result{id: adapter.ID(), sessions: sessions}
		}(i, adapter)
	}
	wg.Wait()

	var all []db.Session
	for _, r := range results {
		if r.sessions == nil {
			continue
		}
		if remember != nil {
			remember(r.id, r.sessions)
		}
		all = append(all, r.sessions...)
	}
	return all
}
