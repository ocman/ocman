package remote

import (
	"context"
	"sync"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/gitexec"
)

// originCache caches a directory's git origin URL so the Projects RPC
// doesn't shell out to git on every call. Entries are kept for the
// process lifetime; an origin rarely changes for a given checkout.
type originCache struct {
	mu sync.Mutex
	m  map[string]string
}

func newOriginCache() *originCache { return &originCache{m: make(map[string]string)} }

// origin returns the cached origin for dir, computing it once on miss.
// A directory with no origin caches the empty string so the lookup is
// not retried on every inventory refresh.
func (c *originCache) origin(ctx context.Context, dir string) string {
	c.mu.Lock()
	if v, ok := c.m[dir]; ok {
		c.mu.Unlock()
		return v
	}
	c.mu.Unlock()

	cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	out, err := gitexec.Output(cctx, dir, "remote", "get-url", "origin")
	origin := ""
	if err == nil {
		origin = out
	}

	c.mu.Lock()
	c.m[dir] = origin
	c.mu.Unlock()
	return origin
}

// projectIdentities builds origin-enriched ProjectIdentity records for a
// host's project stats using the given origin cache (AD-8/AD-9).
func projectIdentities(ctx context.Context, cache *originCache, stats []db.ProjectStats) []ProjectIdentity {
	out := make([]ProjectIdentity, 0, len(stats))
	for _, p := range stats {
		origin := cache.origin(ctx, p.Directory)
		out = append(out, ProjectIdentity{
			Key:      NormalizeProjectIdentity(origin, p.Directory),
			Origin:   origin,
			Basename: basenameOf(p.Directory),
			Dir:      p.Directory,
		})
	}
	return out
}
