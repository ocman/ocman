package opencode

import (
	"context"
	"sync"
)

// setDiscoverPortsImplForTests installs fn as the seam and returns a
// restore func that re-installs the previous value.
func setDiscoverPortsImplForTests(fn func() map[string]string) func() {
	wrapper := func(context.Context) map[string]string {
		ports := fn()
		if ports == nil {
			return map[string]string{}
		}
		return ports
	}
	prev := discoverPortsOverride.Swap(&wrapper)
	return func() { discoverPortsOverride.Store(prev) }
}

func setDiscoverServersImplForTests(fn func() []openCodeServer) func() {
	prev := discoverServersOverride.Swap(&fn)
	return func() { discoverServersOverride.Store(prev) }
}

// put stores a successful response at the port's current generation.
func (c *httpCache) put(port, path string, body []byte) {
	c.putForGeneration(port, path, body, c.generation(port))
}

// signalingContext closes waiting the first time a caller selects on Done,
// so a test can tell a goroutine has reached its wait.
type signalingContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (c *signalingContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

// invalidate drops a single (port, path) entry. Useful after a
// mutating op that would change the upstream response. Currently
// unused; provided for future call sites.
func (c *httpCache) invalidate(port, path string) {
	c.mu.Lock()
	_, existed := c.entries[httpCacheKey{port, path}]
	delete(c.entries, httpCacheKey{port, path})
	c.mu.Unlock()
	if existed {
		c.metrics.RecordEvictions(context.Background(), 1)
	}
}
