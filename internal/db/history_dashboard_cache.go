package db

import (
	"context"
	"sync"
	"time"
)

// Dashboard results are cached per history reader. Every content change
// appends a change_outbox row, so a snapshot's maximum sequence names
// the exact history it reads. Entries hold only unpriced, identity-free data;
// pricing, session identity and the time-dependent series are applied per
// request.
const (
	dashboardCacheEntries = 16
	dashboardCachePages   = 8
)

type dashboardCacheKey struct {
	revision     int64
	zone         string // calendar labels use the local time zone
	dir          string
	days         int
	agent, model string
}

// dashboardEntry is one window's summaries for one set of filters. Lazily
// filled parts are guarded by mu.
type dashboardEntry struct {
	since  int64
	window *dashboardWindow
	groups []dashboardGroup
	used   uint64

	mu      sync.Mutex
	ranked  bool
	overall durationPercentiles
	byLabel map[string]durationPercentiles
	pages   map[[2]int][]pageRow
}

type dashboardCache struct {
	mu      sync.Mutex
	clock   uint64
	entries map[dashboardCacheKey]*dashboardEntry
}

// lookup returns a cached entry for key. An entry built for another since
// still applies when no request lies between the two cutoffs; reuse checks
// that with one indexed probe.
func (c *dashboardCache) lookup(key dashboardCacheKey, since int64, sameFacts func(from, to int64) (bool, error)) (*dashboardEntry, error) {
	c.mu.Lock()
	e := c.entries[key]
	if e != nil {
		c.clock++
		e.used = c.clock
	}
	c.mu.Unlock()
	if e == nil || e.since == since {
		return e, nil
	}
	same, err := sameFacts(min(e.since, since), max(e.since, since))
	if err != nil || !same {
		return nil, err
	}
	return e, nil
}

// store keeps e, dropping entries for older revisions and then the least
// recently used entries beyond the bound.
func (c *dashboardCache) store(key dashboardCacheKey, e *dashboardEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[dashboardCacheKey]*dashboardEntry{}
	}
	for k := range c.entries {
		if k.revision < key.revision {
			delete(c.entries, k)
		}
	}
	for len(c.entries) >= dashboardCacheEntries {
		var oldest dashboardCacheKey
		var used uint64
		first := true
		for k, v := range c.entries {
			if first || v.used < used {
				oldest, used, first = k, v.used, false
			}
		}
		delete(c.entries, oldest)
	}
	c.clock++
	e.used = c.clock
	c.entries[key] = e
}

func (c *dashboardCache) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// loadDashboard returns the window summaries and filtered groups, from the
// reader's cache when this snapshot's history revision was already read.
func (d *DB) loadDashboard(ctx context.Context, opts MetricsDashboardOptions) (*dashboardEntry, error) {
	since := max(0, opts.Since)
	cache, key := d.dashboardCache(opts)
	if cache != nil {
		e, err := cache.lookup(key, since, func(from, to int64) (bool, error) {
			var found bool
			err := d.reads().QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM history_fact
				WHERE role='assistant' AND valid=1 AND time_created>=? AND time_created<?)`, from, to).Scan(&found)
			return !found, err
		})
		if err != nil || e != nil {
			return e, err
		}
	}
	w, err := d.loadDashboardWindow(ctx, opts)
	if err != nil {
		return nil, err
	}
	e := &dashboardEntry{since: since, window: w, groups: w.filtered(opts)}
	if cache != nil {
		cache.store(key, e)
	}
	return e, nil
}

// dashboardCache returns the cache and key for this reader, or nil when the
// reader is not bound to a revision-validated history snapshot.
func (d *DB) dashboardCache(opts MetricsDashboardOptions) (*dashboardCache, dashboardCacheKey) {
	if d.historyScope == nil || d.snapshot == nil || !d.revisionKnown {
		return nil, dashboardCacheKey{}
	}
	return &d.historyScope.dashboards, dashboardCacheKey{revision: d.revision, zone: time.Local.String(), dir: opts.Dir, days: opts.Days, agent: opts.AgentFilter, model: opts.ModelFilter}
}

func (d *DB) entryPercentiles(ctx context.Context, e *dashboardEntry, opts MetricsDashboardOptions) (durationPercentiles, map[string]durationPercentiles, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.ranked {
		overall, byLabel, err := d.durationPercentiles(ctx, e.window, opts, e.groups)
		if err != nil {
			return overall, byLabel, err
		}
		e.overall, e.byLabel, e.ranked = overall, byLabel, true
	}
	return e.overall, e.byLabel, nil
}

func (d *DB) entryPage(ctx context.Context, e *dashboardEntry, opts MetricsDashboardOptions) ([]RequestLogEntry, error) {
	key := [2]int{max(0, opts.RequestLimit), max(0, opts.RequestOffset)}
	e.mu.Lock()
	rows, ok := e.pages[key]
	e.mu.Unlock()
	if !ok {
		var err error
		if rows, err = d.requestRows(ctx, opts, e.groups); err != nil {
			return nil, err
		}
		e.mu.Lock()
		if e.pages == nil || len(e.pages) >= dashboardCachePages {
			e.pages = map[[2]int][]pageRow{}
		}
		e.pages[key] = rows
		e.mu.Unlock()
	}
	return finishPage(ctx, rows, opts)
}
