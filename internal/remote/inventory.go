package remote

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/gitexec"
)

// originCache caches all fetch remotes for five minutes so inventory reads
// avoid repeated subprocesses while remote configuration changes take effect.
type originCache struct {
	mu      sync.Mutex
	m       map[string]cachedUpstreams
	refresh singleflight.Group
	read    func(context.Context, string) (string, error) // nil uses gitexec
}

type cachedUpstreams struct {
	origin  string
	keys    []string
	expires time.Time
}

func newOriginCache() *originCache { return &originCache{m: make(map[string]cachedUpstreams)} }

func (c *originCache) upstreams(ctx context.Context, dir string) (cachedUpstreams, error) {
	value, err, _ := c.refresh.Do(dir, func() (any, error) {
		return c.discover(ctx, dir)
	})
	return value.(cachedUpstreams), err
}

func (c *originCache) discover(ctx context.Context, dir string) (cachedUpstreams, error) {
	c.mu.Lock()
	previous, ok := c.m[dir]
	if ok && time.Now().Before(previous.expires) {
		c.mu.Unlock()
		return previous, nil
	}
	c.mu.Unlock()

	cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var out string
	var err error
	if c.read != nil {
		out, err = c.read(cctx, dir)
	} else {
		out, err = gitexec.Output(cctx, dir, "remote", "-v")
	}
	if err != nil {
		if cctx.Err() != nil {
			return previous, cctx.Err()
		}
		// Historic sessions can refer to non-repositories or deleted checkouts.
		// Known absences use the last value, or directory identity on a first
		// miss. Do not cache absence: a checkout may be restored at any time.
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			return previous, err
		}
		missing := strings.Contains(string(exit.Stderr), "not a git repository") ||
			(strings.Contains(string(exit.Stderr), "cannot change to") && strings.Contains(string(exit.Stderr), "No such file or directory"))
		if !missing {
			return previous, err
		}
		return previous, nil
	}
	v := cachedUpstreams{expires: time.Now().Add(5 * time.Minute)}
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

	c.mu.Lock()
	c.m[dir] = v
	c.mu.Unlock()
	return v, nil
}

var projectUpstreamsCache = newOriginCache()

// EnrichProjectStats runs on the owning host, including for remote inventories.
func EnrichProjectStats(ctx context.Context, stats []db.ProjectStats) error {
	for i := range stats {
		upstreams, err := projectUpstreamsCache.upstreams(ctx, stats[i].Directory)
		if err != nil {
			return fmt.Errorf("discovering project upstreams: %w", err)
		}
		stats[i].UpstreamKeys = slices.Clone(upstreams.keys)
		stats[i].UpstreamOrigin = NormalizeUpstream(upstreams.origin)
	}
	return nil
}

// projectIdentities projects the owner's enriched snapshot without discovery.
func projectIdentities(stats []db.ProjectStats) []ProjectIdentity {
	out := make([]ProjectIdentity, 0, len(stats))
	for _, p := range stats {
		origin := p.UpstreamOrigin
		key := origin
		if key == "" && len(p.UpstreamKeys) > 0 {
			key = p.UpstreamKeys[0]
		}
		if key == "" {
			key = NormalizeProjectIdentity("", p.Directory)
		}
		out = append(out, ProjectIdentity{
			Key:            key,
			Origin:         origin,
			Basename:       basenameOf(p.Directory),
			Dir:            p.Directory,
			UpstreamKeys:   slices.Clone(p.UpstreamKeys),
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
