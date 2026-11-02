package claudeprovider

import (
	"context"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

// Hooks refresh appends immediately. The full sweep recovers missed hooks,
// unsupported watches, and external sessions without the hook plugin.
const indexRecoveryInterval = 5 * time.Minute

func (x *Index) runIndex(ctx context.Context, sweep time.Duration) {
	if sweep <= 0 {
		sweep = indexRecoveryInterval
	}
	wake := make(chan struct{}, 1)
	var mu sync.Mutex
	pending := map[string]bool{}
	if w, err := newDirWatcher(func(path string) {
		mu.Lock()
		if len(pending) >= 4096 {
			pending = map[string]bool{"": true}
		} else {
			pending[path] = true
		}
		mu.Unlock()
		select {
		case wake <- struct{}{}:
		default:
		}
	}); err == nil {
		x.mu.Lock()
		x.watch = w.Add
		x.mu.Unlock()
		defer w.Close()
	}
	report := func(err error) {
		if err != nil && ctx.Err() == nil {
			log.WithError(err).Warn("syncing claude transcript index")
		}
	}
	report(x.Sync(ctx))
	ticker := time.NewTicker(sweep)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			report(x.Sync(ctx))
		case <-wake:
			timer := time.NewTimer(200 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			mu.Lock()
			paths := make([]string, 0, len(pending))
			for path := range pending {
				paths = append(paths, path)
			}
			pending = map[string]bool{}
			mu.Unlock()
			report(x.syncChanges(ctx, paths))
		}
	}
}
