package remote

import (
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/gitexec"
)

// originCache caches all fetch remotes for five minutes so inventory reads
// avoid repeated subprocesses while remote configuration changes take effect.
type originCache struct {
	mu sync.Mutex
	m  map[string]cachedUpstreams
}

type cachedUpstreams struct {
	origin  string
	keys    []string
	expires time.Time
}

func newOriginCache() *originCache { return &originCache{m: make(map[string]cachedUpstreams)} }

func (c *originCache) upstreams(ctx context.Context, dir string) cachedUpstreams {
	c.mu.Lock()
	if v, ok := c.m[dir]; ok && time.Now().Before(v.expires) {
		c.mu.Unlock()
		return v
	}
	c.mu.Unlock()

	cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	out, err := gitexec.Output(cctx, dir, "remote", "-v")
	v := cachedUpstreams{expires: time.Now().Add(5 * time.Minute)}
	if err == nil {
		for _, line := range strings.Split(out, "\n") {
			fields := strings.Fields(line)
			if len(fields) != 3 || fields[2] != "(fetch)" {
				continue
			}
			if fields[0] == "origin" {
				v.origin = fields[1]
			}
			if key := NormalizeUpstream(fields[1]); key != "" {
				v.keys = append(v.keys, key)
			}
		}
		slices.Sort(v.keys)
		v.keys = slices.Compact(v.keys)
	}

	c.mu.Lock()
	c.m[dir] = v
	c.mu.Unlock()
	return v
}

var projectUpstreamsCache = newOriginCache()

// EnrichProjectStats runs on the owning host, including for remote inventories.
func EnrichProjectStats(ctx context.Context, stats []db.ProjectStats) {
	for i := range stats {
		stats[i].UpstreamKeys = slices.Clone(projectUpstreamsCache.upstreams(ctx, stats[i].Directory).keys)
	}
}

// projectIdentities builds origin-enriched ProjectIdentity records for a
// host's project stats using the given origin cache (AD-8/AD-9).
func projectIdentities(ctx context.Context, cache *originCache, stats []db.ProjectStats) []ProjectIdentity {
	out := make([]ProjectIdentity, 0, len(stats))
	for _, p := range stats {
		upstreams := cache.upstreams(ctx, p.Directory)
		origin := NormalizeUpstream(upstreams.origin)
		key := origin
		if key == "" && len(upstreams.keys) > 0 {
			key = upstreams.keys[0]
		}
		if key == "" {
			key = NormalizeProjectIdentity("", p.Directory)
		}
		out = append(out, ProjectIdentity{
			Key:            key,
			Origin:         origin,
			Basename:       basenameOf(p.Directory),
			Dir:            p.Directory,
			UpstreamKeys:   slices.Clone(upstreams.keys),
			SessionCount:   p.SessionCount,
			MessageCount:   p.MessageCount,
			LastUsed:       p.LastUsed,
			TotalTokensIn:  p.TotalTokensIn,
			TotalTokensOut: p.TotalTokensOut,
			TotalCost:      p.TotalCost,
		})
	}
	return out
}
