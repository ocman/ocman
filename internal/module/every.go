package module

import (
	"context"
	"runtime/debug"
	"time"

	log "github.com/sirupsen/logrus"
)

// RunWithRecover runs body, logging a panic with the loop name and stack
// instead of propagating it, so one bad iteration cannot disable a
// background feature for the rest of the process.
func RunWithRecover(name string, body func()) {
	defer func() {
		if r := recover(); r != nil {
			log.WithFields(log.Fields{
				"loop":  name,
				"panic": r,
				"stack": string(debug.Stack()),
			}).Error("background loop panicked, continuing on next tick")
		}
	}()
	body()
}

// Every runs fn now and then interval after each run finishes, until ctx
// ends. Each run is recovered with RunWithRecover.
func Every(ctx context.Context, name string, interval time.Duration, fn func(context.Context)) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		RunWithRecover(name, func() { fn(ctx) })
		ticker.Reset(interval) // no queued catch-up run after a slow one
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
