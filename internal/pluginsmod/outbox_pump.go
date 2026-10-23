package pluginsmod

import (
	"context"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/module"
)

// The conversation reply and session.v1 completion outboxes share one
// delivery schedule.
const (
	// outboxDeliveryBatch bounds one pump pass. Only one delivery per ordering
	// group is ever claimed, so this bounds concurrent conversations/sessions.
	outboxDeliveryBatch = 32
	// outboxRetryBase and outboxRetryMax bound the retry schedule: 5s doubling
	// to a 5 minute ceiling. A provider asking for a longer wait (Slack's
	// Retry-After) is absorbed inside the plugin, which knows the provider's
	// limits; the host only guarantees the backoff stays bounded.
	outboxRetryBase = 5 * time.Second
	outboxRetryMax  = 5 * time.Minute
	// outboxMaxAttempts is where a poison delivery stops consuming its
	// ordering slot and becomes a visible dead letter.
	outboxMaxAttempts = 6
	// outboxPumpInterval wakes deliveries whose backoff has elapsed. A fresh
	// row kicks the pump directly, so this is only the retry clock and the
	// recovery path after a restart.
	outboxPumpInterval = 5 * time.Second
	// outboxPruneInterval drops delivered receipts past their retention.
	outboxPruneInterval = time.Hour
	// outboxReconcileInterval is the backstop for OpenCode instances that omit
	// terminal idle/changed events.
	outboxReconcileInterval = 5 * time.Minute
)

// outboxRetryDelay is exponential with a hard ceiling. attempts is the number
// of failures already recorded, so the first retry waits the base.
func outboxRetryDelay(attempts int) time.Duration {
	delay := outboxRetryBase
	for range attempts {
		if delay >= outboxRetryMax/2 {
			return outboxRetryMax
		}
		delay *= 2
	}
	return delay
}

// outbox is the in-process side of one durable plugin outbox: the pump that
// admits due rows to the delivery worker, its retry/prune clock and kicks.
type outbox[T any] struct {
	// name labels the pump loop ("plugin-<name>-pump").
	name string
	// claim offers due head rows to admit, in order, until a batch is admitted.
	claim func(ctx context.Context, admit func(T) bool)
	// row identifies a row and the plugin it is owed to.
	row func(T) (id int64, pluginID string)
	// allowed reports whether the plugin may be delivered to right now.
	allowed func(ctx context.Context, pluginID string) bool
	enqueue func(T)
	// prune drops delivered receipts; pruneFailed is its warning.
	prune       func(ctx context.Context) error
	pruneFailed string
	workers     *module.Workers

	// inFlight keeps a pump tick during a slow send from delivering the same
	// row twice. It is not state.
	mu       sync.Mutex
	inFlight map[int64]bool
}

// pump enqueues every due head row that is not already in flight.
func (o *outbox[T]) pump(ctx context.Context) {
	allowed := map[string]bool{}
	o.claim(ctx, func(row T) bool {
		id, pluginID := o.row(row)
		if _, asked := allowed[pluginID]; !asked {
			allowed[pluginID] = o.allowed(ctx, pluginID)
		}
		if !allowed[pluginID] || !o.begin(id) {
			return false
		}
		o.enqueue(row)
		return true
	})
}

// kick pumps without waiting for the next tick.
func (o *outbox[T]) kick() {
	o.workers.Go(func(ctx context.Context) {
		runWithRecover("plugin-"+o.name+"-pump", func() { o.pump(ctx) })
	})
}

// run is the retry clock and the crash-recovery path: every unacknowledged row
// left by a disconnect or a restart is claimed again here. One immediate pass
// runs at startup, before the first tick.
func (o *outbox[T]) run(ctx context.Context) {
	tick := time.NewTicker(outboxPumpInterval)
	defer tick.Stop()
	prune := time.NewTicker(outboxPruneInterval)
	defer prune.Stop()
	o.pump(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			o.pump(ctx)
		case <-prune.C:
			if err := o.prune(ctx); err != nil {
				log.WithError(err).Warn(o.pruneFailed)
			}
		}
	}
}

func (o *outbox[T]) begin(id int64) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.inFlight[id] {
		return false
	}
	if o.inFlight == nil {
		o.inFlight = make(map[int64]bool)
	}
	o.inFlight[id] = true
	return true
}

func (o *outbox[T]) end(id int64) {
	o.mu.Lock()
	delete(o.inFlight, id)
	o.mu.Unlock()
}

// runEvery runs fn now and then every interval until ctx ends.
func runEvery(ctx context.Context, interval time.Duration, fn func(context.Context)) {
	fn(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			fn(ctx)
		}
	}
}
