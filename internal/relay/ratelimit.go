package relay

import (
	"sync"
	"time"
)

// rateLimiter is a per-key token bucket.
//
// ponytail: an in-process map, so limits are per relay process rather
// than global. That is the right trade for a single-instance relay; if
// the relay is ever run behind a load balancer with several replicas,
// move this to a shared counter.
type rateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	// refill is the number of tokens added per second.
	refill float64
	// burst is the bucket capacity.
	burst float64
	now   func() time.Time
}

type bucket struct {
	tokens float64
	seen   time.Time
}

// newRateLimiter returns a limiter allowing `perHour` events per key
// with the given burst capacity.
func newRateLimiter(perHour, burst float64, now func() time.Time) *rateLimiter {
	if now == nil {
		now = time.Now
	}
	return &rateLimiter{
		buckets: map[string]*bucket{},
		refill:  perHour / 3600,
		burst:   burst,
		now:     now,
	}
}

// allow consumes one token for a key, reporting whether it was
// available.
func (l *rateLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: l.burst, seen: now}
		l.buckets[key] = b
	} else {
		b.tokens = min(l.burst, b.tokens+now.Sub(b.seen).Seconds()*l.refill)
		b.seen = now
	}
	l.evictLocked(now)

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// evictLocked drops buckets that have been idle long enough to have
// refilled completely, so the map cannot grow without bound.
func (l *rateLimiter) evictLocked(now time.Time) {
	if len(l.buckets) < 1024 {
		return
	}
	var full time.Duration
	if l.refill > 0 {
		full = time.Duration(l.burst/l.refill) * time.Second
	}
	for k, b := range l.buckets {
		if now.Sub(b.seen) > full {
			delete(l.buckets, k)
		}
	}
}
