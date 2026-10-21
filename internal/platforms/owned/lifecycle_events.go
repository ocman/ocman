package owned

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
	log "github.com/sirupsen/logrus"
)

type lifecycleEvents struct {
	mu       sync.Mutex
	sink     platforms.SessionEventSink
	pending  map[string]lifecycleEvent
	flushing bool
}

type lifecycleEvent struct {
	status          db.SessionStatus
	resetsAt        int64
	hasStatus, idle bool
}

func (a *Adapter) SetSessionEventSink(sink platforms.SessionEventSink) {
	a.lifecycle.mu.Lock()
	a.lifecycle.sink = sink
	a.lifecycle.mu.Unlock()
	if source, ok := a.native.(platforms.SessionEventSource); ok {
		source.SetSessionEventSink(platforms.SessionEventSink{
			OnSessionStatus: func(platform, id string, status db.SessionStatus, resetsAt int64) {
				a.receiveLifecycle(platform, id, lifecycleEvent{status: status, resetsAt: resetsAt, hasStatus: true})
			},
			OnSessionIdle: func(platform, id string) { a.receiveLifecycle(platform, id, lifecycleEvent{idle: true}) },
		})
	}
}

func (a *Adapter) receiveLifecycle(platform, nativeID string, event lifecycleEvent) {
	if platform != string(a.ID()) || nativeID == "" {
		return
	}
	a.lifecycle.mu.Lock()
	sink := a.lifecycle.sink
	if (event.hasStatus && sink.OnSessionStatus == nil) || (event.idle && sink.OnSessionIdle == nil) {
		a.lifecycle.mu.Unlock()
		return
	}
	if a.lifecycle.pending == nil {
		a.lifecycle.pending = map[string]lifecycleEvent{}
	}
	previous, known := a.lifecycle.pending[nativeID]
	if !known && len(a.lifecycle.pending) >= 4096 {
		a.lifecycle.mu.Unlock()
		log.WithField("platform", a.ID()).Warn("canonical lifecycle buffer full; owner lifecycle sweep must reconcile")
		return
	}
	if event.hasStatus {
		previous.status, previous.resetsAt, previous.hasStatus = event.status, event.resetsAt, true
		if event.status == db.StatusBusy || event.status == db.StatusLimited {
			previous.idle = false
		}
	}
	if event.idle {
		previous.idle = true
	}
	a.lifecycle.pending[nativeID] = previous
	a.lifecycle.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a.flushLifecycle(ctx)
}

// flushLifecycle never probes native content while an adapter's event callback
// may hold its own locks. Metadata-only adoption and creation reservations own
// the mapping. Pending creation events retry on commit and history inventories.
func (a *Adapter) flushLifecycle(ctx context.Context) {
	a.lifecycle.mu.Lock()
	if a.lifecycle.flushing || len(a.lifecycle.pending) == 0 {
		a.lifecycle.mu.Unlock()
		return
	}
	a.lifecycle.flushing = true
	pending := a.lifecycle.pending
	a.lifecycle.pending = map[string]lifecycleEvent{}
	sink := a.lifecycle.sink
	a.lifecycle.mu.Unlock()
	deferred := map[string]lifecycleEvent{}
	reported := false
	for nativeID, event := range pending {
		row, err := a.historyIdentity(ctx, nativeID)
		if err != nil {
			deferred[nativeID] = event
			if !reported && !errors.Is(err, sessioncatalog.ErrCreating) {
				log.WithError(err).WithField("platform", a.ID()).Warn("canonical lifecycle identity unavailable; retaining event for retry")
				reported = true
			}
			continue
		}
		if row.Purged {
			continue
		}
		if event.hasStatus && sink.OnSessionStatus != nil {
			sink.OnSessionStatus(string(a.ID()), row.ID, event.status, event.resetsAt)
		}
		if event.idle && sink.OnSessionIdle != nil {
			sink.OnSessionIdle(string(a.ID()), row.ID)
		}
	}
	a.lifecycle.mu.Lock()
	arrived := len(a.lifecycle.pending) > 0
	for nativeID, event := range deferred {
		if latest, ok := a.lifecycle.pending[nativeID]; ok {
			if !latest.hasStatus {
				latest.status, latest.resetsAt, latest.hasStatus = event.status, event.resetsAt, event.hasStatus
			}
			if event.idle && (!latest.hasStatus || (latest.status != db.StatusBusy && latest.status != db.StatusLimited)) {
				latest.idle = true
			}
			a.lifecycle.pending[nativeID] = latest
		} else if len(a.lifecycle.pending) < 4096 {
			a.lifecycle.pending[nativeID] = event
		}
	}
	a.lifecycle.flushing = false
	a.lifecycle.mu.Unlock()
	if arrived && ctx.Err() == nil {
		a.flushLifecycle(ctx)
	}
}

var _ platforms.SessionEventSource = (*Adapter)(nil)
