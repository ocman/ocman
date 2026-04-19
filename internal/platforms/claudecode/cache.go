package claudecode

import "sync"

// cacheEntry holds a parsed jsonl file. The file's (mtime, size) pair
// is the invalidation key: if the file is appended to or rewritten,
// either value changes and the next access re-parses.
type cacheEntry struct {
	mtimeMs int64 // file ModTime as Unix milliseconds
	size    int64
	value   *parsedFile
}

// cache memoises parse results across Sessions() / Session() calls
// within a single process. No disk persistence; the process is
// short-lived enough for mtime-keyed invalidation to be the right
// trade-off (AD-3).
//
// Entries are keyed by absolute file path. Max 20 entries to prevent
// unbounded memory growth.
type cache struct {
	mu      sync.RWMutex
	entries map[string]*cacheEntry
	order   []string
}

const maxCacheEntries = 20

// newCache returns an empty cache.
func newCache() *cache {
	return &cache{
		entries: make(map[string]*cacheEntry),
		order:   make([]string, 0, maxCacheEntries),
	}
}

// getByMtime returns the cached parsed value for path if and only if
// the cache key (mtime, size) matches the caller's observed values.
// On a miss or a stale entry, returns nil.
func (c *cache) getByMtime(path string, mtimeMs, size int64) *parsedFile {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.entries[path]
	if !ok || entry.value == nil {
		return nil
	}
	if entry.mtimeMs != mtimeMs || entry.size != size {
		return nil
	}
	return entry.value
}

// putByMtime stores a parsed value under path with the given
// invalidation key. Overwrites any prior entry for the same path.
// Enforces maxCacheEntries with LRU eviction.
func (c *cache) putByMtime(path string, mtimeMs, size int64, value *parsedFile) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// If updating existing, move to end (most recent)
	if _, exists := c.entries[path]; exists {
		c.entries[path] = &cacheEntry{mtimeMs: mtimeMs, size: size, value: value}
		return
	}

	// Evict oldest if at capacity
	for len(c.order) >= maxCacheEntries {
		if len(c.order) > 0 {
			evict := c.order[0]
			delete(c.entries, evict)
			c.order = c.order[1:]
		}
	}

	c.entries[path] = &cacheEntry{mtimeMs: mtimeMs, size: size, value: value}
	c.order = append(c.order, path)
}

// forget drops a cached entry. Called when the caller observes that
// the file no longer exists.
func (c *cache) forget(path string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, path)
	// Remove from order slice
	for i, p := range c.order {
		if p == path {
			c.order = append(c.order[:i], c.order[i+1:]...)
			break
		}
	}
}

// len returns the number of cached entries; used by tests.
func (c *cache) len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}
