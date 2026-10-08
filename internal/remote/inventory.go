package remote

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/singleflight"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/gitexec"
	"github.com/NoUseFreak/ocman/internal/telemetry"
)

const (
	// upstreamTTL sits far above the 5-minute projects-index tick so an
	// unchanged repository is probed once per hour instead of once per
	// tick. It only bounds changes invisible to gitConfigProbe (e.g.
	// remotes pulled in from global config); remote edits in the
	// repository config are picked up on the next tick via the file's
	// modification time.
	upstreamTTL = time.Hour
	// nonRepoRecheck bounds how long a directory last seen as a
	// non-repository stays cached before a git init in it is detected.
	nonRepoRecheck = 15 * time.Minute
	// enrichParallelism caps concurrent git lookups per inventory
	// refresh; gitexec additionally caps machine-wide concurrency.
	enrichParallelism = 8
)

// originCache caches all fetch remotes per directory. Positive entries
// live for upstreamTTL and are invalidated cheaply by the repository
// config's mtime; absent directories are cached until os.Stat sees the
// directory again, so deleted checkouts stop costing a subprocess each.
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
	// gone marks a directory the last probe could not access (deleted
	// checkout). While os.Stat keeps failing the entry is served from
	// cache without a git call; when the directory reappears it is
	// re-probed on the next call.
	gone bool
	// configPath/configMod record the file holding the remotes and its
	// mtime at probe time; a change re-probes on the next call. An
	// empty path (unresolvable) falls back to TTL expiry only.
	configPath string
	configMod  time.Time
}

func newOriginCache() *originCache { return &originCache{m: make(map[string]cachedUpstreams)} }

func (c *originCache) upstreams(ctx context.Context, dir string) (cachedUpstreams, error) {
	value, err, _ := c.refresh.Do(dir, func() (any, error) {
		return c.discover(ctx, dir)
	})
	return value.(cachedUpstreams), err
}

// fresh reports whether a cached entry may be served without a git call.
// Deleted directories stay cached until the directory reappears; remote
// edits invalidate a positive entry through the config file's mtime.
func (c *originCache) fresh(e cachedUpstreams, dir string) bool {
	_, statErr := os.Stat(dir)
	if e.gone {
		return statErr != nil
	}
	if statErr != nil || !time.Now().Before(e.expires) {
		return false
	}
	if e.configPath != "" {
		if st, err := os.Stat(e.configPath); err != nil || !st.ModTime().Equal(e.configMod) {
			return false
		}
	}
	return true
}

func (c *originCache) discover(ctx context.Context, dir string) (cachedUpstreams, error) {
	c.mu.Lock()
	previous, ok := c.m[dir]
	c.mu.Unlock()
	if ok && c.fresh(previous, dir) {
		return previous, nil
	}

	cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	cctx, span := telemetry.Tracer().Start(cctx, "ocman.projects_index.git_lookup")
	defer span.End()
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
		// miss, and are cached: a deleted directory is re-probed only once
		// os.Stat sees it again, a non-repository every nonRepoRecheck.
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			return previous, err
		}
		stderr := string(exit.Stderr)
		gone := strings.Contains(stderr, "cannot change to") && strings.Contains(stderr, "No such file or directory")
		if !gone && !strings.Contains(stderr, "not a git repository") {
			return previous, err
		}
		v := previous
		v.gone = gone
		v.configPath, v.configMod = "", time.Time{}
		if !gone {
			v.expires = time.Now().Add(nonRepoRecheck)
		}
		c.mu.Lock()
		c.m[dir] = v
		c.mu.Unlock()
		return v, nil
	}
	v := cachedUpstreams{expires: time.Now().Add(upstreamTTL)}
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
	v.configPath, v.configMod = gitConfigProbe(cctx, dir)

	c.mu.Lock()
	c.m[dir] = v
	c.mu.Unlock()
	return v, nil
}

// gitConfigProbe locates the config file that defines the directory's
// remotes via git itself, so linked worktrees (whose remotes live in the
// main repository's config) and repository subdirectories resolve
// correctly. An empty path means git could not answer and the entry
// falls back to TTL expiry.
func gitConfigProbe(ctx context.Context, dir string) (string, time.Time) {
	common, err := gitexec.Output(ctx, dir, "rev-parse", "--git-common-dir")
	if err != nil || common == "" {
		return "", time.Time{}
	}
	if !filepath.IsAbs(common) {
		common = filepath.Join(dir, common)
	}
	path := filepath.Join(common, "config")
	if st, err := os.Stat(path); err == nil {
		return path, st.ModTime()
	}
	return "", time.Time{}
}

var projectUpstreamsCache = newOriginCache()

// EnrichProjectStats runs on the owning host, including for remote inventories.
func EnrichProjectStats(ctx context.Context, stats []db.ProjectStats) error {
	if len(stats) == 0 {
		return nil
	}
	ctx, span := telemetry.Tracer().Start(ctx, "ocman.projects_index.git_lookups")
	defer span.End()
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(enrichParallelism)
	for i := range stats {
		g.Go(func() error {
			upstreams, err := projectUpstreamsCache.upstreams(ctx, stats[i].Directory)
			if err != nil {
				return err
			}
			stats[i].UpstreamKeys = slices.Clone(upstreams.keys)
			stats[i].UpstreamOrigin = NormalizeUpstream(upstreams.origin)
			return nil
		})
	}
	err := g.Wait()
	span.SetAttributes(
		attribute.Int("ocman.projects.directories", len(stats)),
	)
	if err != nil {
		span.RecordError(err)
		return fmt.Errorf("discovering project upstreams: %w", err)
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
