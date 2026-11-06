package conversationingest

import (
	"context"
	"math/rand/v2"
	"time"
)

type reconnectBackoff struct{ delay time.Duration }

// Interactive reads do not inherit a background failure's cooldown. Serve at
// most one per boundary, on the source worker. Normal drains already serve them.
func (w *sourceWorker) serveFeedPriority(ctx context.Context, retry retryBackoff) {
	w.mu.Lock()
	requested := len(w.priority) > 0
	w.mu.Unlock()
	if requested && !retry.due() && w.ensureInstance(ctx) == nil {
		w.servePriority(ctx)
	}
}

// A successful handshake alone is not healthy: flapping streams still back off.
func (r *reconnectBackoff) after(uptime time.Duration) time.Duration {
	if uptime >= 30*time.Second {
		r.delay = 0
	}
	if r.delay == 0 {
		r.delay = 2 * time.Second
	} else {
		r.delay = min(2*r.delay, 30*time.Second)
	}
	return min(r.delay*3/4+time.Duration(rand.Int64N(int64(r.delay/2))), 30*time.Second)
}

func waitReconnect(ctx context.Context, demand <-chan struct{}, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-demand:
		return ctx.Err() == nil
	case <-timer.C:
		return ctx.Err() == nil
	}
}
