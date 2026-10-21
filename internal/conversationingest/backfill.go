package conversationingest

import (
	"context"
	"time"
)

// The slot and cooldown are shared by all sources. Unlike GOMAXPROCS, this
// only limits historical imports, not HTTP handlers or live event delivery.
type backfillLimiter struct {
	slot    chan struct{}
	percent int
	next    time.Time // protected by slot
	now     func() time.Time
}

func newBackfillLimiter(percent int) *backfillLimiter {
	return &backfillLimiter{slot: make(chan struct{}, 1), percent: percent, now: time.Now}
}

func (l *backfillLimiter) acquire(ctx context.Context, wake <-chan struct{}, attend func()) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-wake:
			attend()
		case l.slot <- struct{}{}:
			return l.cooldown(ctx, wake, attend)
		}
	}
}

func (l *backfillLimiter) cooldown(ctx context.Context, wake <-chan struct{}, attend func()) (func(), error) {
	handedOff := false
	defer func() {
		if !handedOff {
			<-l.slot
		}
	}()
	if delay := l.next.Sub(l.now()); delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		waiting := true
		for waiting {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-wake:
				attend()
			case <-timer.C:
				waiting = false
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	start := l.now()
	handedOff = true
	return func() {
		now := l.now()
		rest := time.Duration(0)
		if l.percent < 100 {
			rest = max(10*time.Millisecond, now.Sub(start)*time.Duration(100-l.percent)/time.Duration(l.percent))
		}
		l.next = now.Add(rest)
		<-l.slot
	}, nil
}

func (w *sourceWorker) beginBackfill(ctx context.Context, background bool, served map[string]bool) (func(), error) {
	if !background {
		return func() {}, ctx.Err()
	}
	attend := func() {
		if id := w.servePriority(ctx); id != "" {
			served[id] = true
		}
	}
	attend()
	woke := false
	release, err := w.module.backfill.acquire(ctx, w.wake, func() {
		woke = true
		attend()
	})
	// A wake can also mean feed activity or a source-wide repair. Keep it for
	// the normal source loop after the historical pass, without a busy loop.
	if woke {
		w.signal()
	}
	return release, err
}
