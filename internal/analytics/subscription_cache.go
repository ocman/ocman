package analytics

import (
	"context"
	"sync"
	"time"

	"github.com/ocman/ocman/internal/module"
	"github.com/ocman/ocman/internal/platforms"
)

const (
	subscriptionResponseBudget = time.Second
	subscriptionRefreshTimeout = 15 * time.Second
	subscriptionUsageTTL       = time.Minute
	subscriptionRetryDelay     = 15 * time.Second
)

// subscriptionRefresh returns a zero value on a transient failure and
// visible=false when the source definitively has no usage to report.
type subscriptionRefresh func(context.Context) (value subscriptionProviderUsage, visible bool)

type subscriptionUsageCache struct {
	workers  module.Workers
	mu       sync.Mutex
	entries  map[platforms.ID]*subscriptionUsageEntry
	typeSafe subscriptionUsageEntry
}

type subscriptionUsageEntry struct {
	value   subscriptionProviderUsage
	hidden  bool
	next    time.Time
	done    chan struct{}
	retired bool
}

type subscriptionUsageRead struct {
	entry    *subscriptionUsageEntry
	done     <-chan struct{}
	fallback subscriptionProviderUsage
}

func unavailableUsage(id, name string) subscriptionProviderUsage {
	return subscriptionProviderUsage{ID: id, Name: name, Status: "upstream_error", Windows: []subscriptionUsageWindow{}}
}

func (c *subscriptionUsageCache) get(ctx context.Context, sources []SubscriptionSource, typeSafe subscriptionRefresh) subscriptionUsageResponse {
	reads := c.prepare(ctx, sources, typeSafe)
	// All refreshes have already started. This is one budget for the entire
	// response, not a separate wait for each provider.
	for _, read := range reads {
		if read.done != nil {
			select {
			case <-read.done:
			case <-ctx.Done():
			}
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	usage := subscriptionUsageResponse{Providers: []subscriptionProviderUsage{}}
	for _, read := range reads {
		if read.entry.hidden || read.entry.retired {
			continue
		}
		value := read.entry.value
		if value.Status == "" {
			value = read.fallback
		} else {
			value.ID, value.Name = read.fallback.ID, read.fallback.Name
			value.PlatformName, value.MachineName = read.fallback.PlatformName, read.fallback.MachineName
		}
		usage.Providers = append(usage.Providers, value)
	}
	return usage
}

func (c *subscriptionUsageCache) prepare(ctx context.Context, sources []SubscriptionSource, typeSafe subscriptionRefresh) []subscriptionUsageRead {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[platforms.ID]*subscriptionUsageEntry)
	}
	reads := make([]subscriptionUsageRead, 0, len(sources)+1)
	active := make(map[platforms.ID]bool, len(sources))
	for _, source := range sources {
		reader, ok := source.Platform.(subscriptionAccountReader)
		if !ok {
			continue
		}
		active[source.Key] = true
		entry := c.entries[source.Key]
		if entry == nil || (entry.retired && entry.done == nil) {
			entry = &subscriptionUsageEntry{}
			c.entries[source.Key] = entry
		}
		if entry.retired {
			// A disabled connection was re-enabled before its old read ended.
			// Do not publish that read or launch a duplicate while it runs.
			entry = &subscriptionUsageEntry{}
		} else if ctx.Err() == nil {
			c.start(entry, subscriptionUsageTTL, func(ctx context.Context) (subscriptionProviderUsage, bool) {
				return fetchPlatformSubscription(ctx, source.Platform, reader)
			})
		}
		reads = append(reads, subscriptionUsageRead{entry, entry.done, source.label(unavailableUsage("", ""))})
	}
	for id, entry := range c.entries {
		if !active[id] {
			entry.retired = true
			if entry.done == nil {
				delete(c.entries, id)
			}
		}
	}
	if typeSafe != nil {
		entry := &c.typeSafe
		if ctx.Err() == nil {
			// Read the local ledger afresh, coalescing only concurrent reads.
			c.start(entry, 0, typeSafe)
		}
		reads = append(reads, subscriptionUsageRead{entry, entry.done, unavailableUsage("typesafe", "TypeSafe")})
	}
	return reads
}

// start runs with mu held, but all I/O uses the module's lifetime and runs
// outside mu. A request timing out does not interrupt a shared refresh.
func (c *subscriptionUsageCache) start(entry *subscriptionUsageEntry, ttl time.Duration, refresh subscriptionRefresh) {
	if entry.done != nil || time.Now().Before(entry.next) {
		return
	}
	done := make(chan struct{})
	if c.workers.Go(func(lifetime context.Context) {
		ctx, cancel := context.WithTimeout(lifetime, subscriptionRefreshTimeout)
		defer cancel()
		var value subscriptionProviderUsage
		visible := true
		module.RunWithRecover("provider usage refresh", func() { value, visible = refresh(ctx) })
		c.mu.Lock()
		defer c.mu.Unlock()
		defer close(done)
		entry.done = nil
		now := time.Now()
		if ctx.Err() != nil || (visible && (value.Status == "" || value.Status == "upstream_error")) {
			entry.next = now.Add(subscriptionRetryDelay)
			return
		}
		if visible {
			value.RefreshedAt = now.UTC().Format(time.RFC3339)
		}
		entry.value, entry.hidden, entry.next = value, !visible, now.Add(ttl)
	}) {
		entry.done = done
	}
}
