package telemetry

import (
	"sync"
	"sync/atomic"

	"go.opentelemetry.io/otel/attribute"
)

const (
	sqlAttributeCacheSize = 256
	sqlAttributeMaxQuery  = 4096
)

// Cache immutable attributes for repeated statement shapes. Bound both entry
// count and key size so dynamic SQL cannot retain unbounded process memory.
// Returned slices are read-only; otelsql copies them into its own attributes.
type sqlAttributeCache struct {
	mu      sync.RWMutex
	entries map[string][]attribute.KeyValue
	keys    [sqlAttributeCacheSize]string
	next    int
}

var queryAttributes sqlAttributeCache

// Before Init, keep instrumentation available to callers that supply their
// own OTel providers. Only explicit application configuration disables it.
var sqlInstrumentationDisabled atomic.Bool

func (c *sqlAttributeCache) get(query string) []attribute.KeyValue {
	if query == "" {
		return nil
	}
	if len(query) > sqlAttributeMaxQuery {
		return buildSQLQueryAttributes(query)
	}
	c.mu.RLock()
	attrs, ok := c.entries[query]
	c.mu.RUnlock()
	if ok {
		return attrs
	}
	attrs = buildSQLQueryAttributes(query)
	c.mu.Lock()
	defer c.mu.Unlock()
	if existing, ok := c.entries[query]; ok {
		return existing
	}
	if c.entries == nil {
		c.entries = make(map[string][]attribute.KeyValue, sqlAttributeCacheSize)
	}
	delete(c.entries, c.keys[c.next])
	c.keys[c.next] = query
	c.next = (c.next + 1) % sqlAttributeCacheSize
	c.entries[query] = attrs
	return attrs
}
