package owned

import (
	"context"
	"encoding/json"
	"maps"
	"time"

	"github.com/google/uuid"
	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/module"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/sidebar"
	"github.com/ocman/ocman/internal/statev2"
	log "github.com/sirupsen/logrus"
)

func (a *Adapter) sidebarScope() sidebar.Scope {
	return sidebar.Scope{Owner: a.owner, Platform: a.source.Platform, Source: a.source.SourceKey, Generation: a.sidebarGeneration}
}

func (a *Adapter) openSidebar(ctx context.Context) (*sidebar.Reader, error) {
	a.sidebarMu.Lock()
	defer a.sidebarMu.Unlock()
	if a.sidebarReader != nil {
		return a.sidebarReader, nil
	}
	core, err := a.store.FilePath(ctx)
	if err != nil {
		return nil, err
	}
	r, err := sidebar.Open(a.history.FilePath(), core)
	if err != nil {
		return nil, err
	}
	a.sidebarGeneration = uuid.NewString()
	if a.source.SourceKey != "" {
		if err := a.history.BeginSidebarGeneration(ctx, a.observationScope(), a.sidebarGeneration); err != nil {
			_ = r.Close()
			return nil, err
		}
	}
	a.sidebarReader = r
	return r, nil
}

func (a *Adapter) SidebarSessions(ctx context.Context, q platforms.SidebarQuery) ([]db.Session, error) {
	r, err := a.openSidebar(ctx)
	if err != nil {
		return nil, err
	}
	return r.Sessions(ctx, a.sidebarScope(), q)
}

func (a *Adapter) SidebarSource(ctx context.Context) (platforms.SidebarSource, error) {
	if _, err := a.openSidebar(ctx); err != nil {
		return platforms.SidebarSource{}, err
	}
	s := a.sidebarScope()
	return platforms.SidebarSource{Owner: s.Owner, Platform: s.Platform, Source: s.Source, Generation: s.Generation}, nil
}

func (a *Adapter) SidebarSessionsFor(ctx context.Context, sources []platforms.SidebarSource, q platforms.SidebarQuery) ([]db.Session, error) {
	r, err := a.openSidebar(ctx)
	if err != nil {
		return nil, err
	}
	scopes := make([]sidebar.Scope, 0, len(sources))
	for _, s := range sources {
		if s.Owner != a.owner {
			return nil, platforms.ErrHistoryUnavailable
		}
		scopes = append(scopes, sidebar.Scope{Owner: s.Owner, Platform: s.Platform, Source: s.Source, Generation: s.Generation})
	}
	return r.SessionsFor(ctx, scopes, q)
}

func (a *Adapter) observationScope() statev2.Instance {
	return statev2.Instance{OwnerID: a.owner, Platform: a.source.Platform, SourceKey: a.source.SourceKey}
}

// SidebarModule owns observation persistence and the read connection lifetime.
// Its worker copies event caches, never transcript listings or supervisor RPCs.
func (a *Adapter) SidebarModule() module.Module { return &sidebarWorker{adapter: a} }

// The worker samples the event cache every sidebarSnapshotInterval so status
// changes reach the sidebar within half a second, but it commits only when the
// observations change. The lease guards against a stalled worker whose last
// checkpoint would otherwise stay live: disconnects, drains and restarts are
// written (or invalidated by a new generation) on the next tick, so the TTL
// only bounds how long a hung worker's state can linger. A 30 s lease renewed
// every 10 s trades that bound (previously 3 s) for one cheap UPDATE per 10 s
// instead of a full replace transaction twice a second.
const (
	sidebarSnapshotInterval = 500 * time.Millisecond
	sidebarLeaseTTL         = 30 * time.Second
	sidebarLeaseRenewEvery  = 10 * time.Second
)

type sidebarWorker struct {
	adapter    *Adapter
	background map[string][2]int
	// written is the generation and encoded observations last committed;
	// leaseUntil is the lease written with them (0 when unavailable).
	written    string
	leaseUntil time.Time
}

func (w *sidebarWorker) Name() string       { return "sidebar-" + string(w.adapter.ID()) }
func (*sidebarWorker) Routes(module.Router) {}
func (w *sidebarWorker) Start(ctx context.Context) error {
	_, err := w.adapter.openSidebar(ctx)
	if ctx.Err() != nil {
		return nil
	}
	return err
}
func (w *sidebarWorker) Close(ctx context.Context) error {
	a := w.adapter
	a.sidebarMu.Lock()
	defer a.sidebarMu.Unlock()
	if a.sidebarReader == nil {
		return nil
	}
	err := a.sidebarReader.Close()
	a.sidebarReader = nil
	return err
}
func (w *sidebarWorker) Finalize() {
	if err := w.Close(context.Background()); err != nil {
		log.WithError(err).Warn("closing sidebar reader")
	}
}
func (w *sidebarWorker) Run(ctx context.Context) {
	ticker := time.NewTicker(sidebarSnapshotInterval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		if err := w.snapshot(ctx); err != nil && ctx.Err() == nil {
			log.WithError(err).WithField("platform", w.adapter.ID()).Warn("persisting sidebar observations")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (w *sidebarWorker) snapshot(ctx context.Context) error {
	a := w.adapter
	if a.unavailable != nil || a.source.SourceKey == "" {
		return nil
	}
	observer, ok := a.native.(platforms.CachedSessionObserver)
	if !ok {
		return nil
	}
	observations := map[string]statev2.SidebarObservation{}
	liveRows := maps.Clone(observer.CachedSessionObservations())
	if liveRows == nil {
		liveRows = map[string]platforms.LiveState{}
	}
	if err := a.backgroundObservations(ctx, liveRows); err != nil {
		return err
	}
	for id, live := range liveRows {
		observations[id] = statev2.SidebarObservation{Status: live.Status, ResetsAt: live.ResetsAt, PendingPermission: live.PendingPermission, PendingQuestion: live.PendingQuestion, BackgroundTasks: live.BackgroundTaskCount, BackgroundServices: live.BackgroundServiceCount}
	}
	now := time.Now()
	leaseUntil := now.Add(sidebarLeaseTTL)
	if source, ok := a.native.(platforms.CachedSessionAvailability); ok && !source.CachedSessionAvailable() {
		leaseUntil = time.Time{}
	}
	raw, err := json.Marshal(observations)
	if err != nil {
		return err
	}
	written := a.sidebarGeneration + "\x00" + string(raw)
	switch {
	case written != w.written || leaseUntil.IsZero() != w.leaseUntil.IsZero():
		if err := a.history.ReplaceSidebarObservations(ctx, a.observationScope(), a.sidebarGeneration, leaseMillis(leaseUntil), observations); err != nil {
			w.written = ""
			return err
		}
		w.written, w.leaseUntil = written, leaseUntil
	case !leaseUntil.IsZero() && w.leaseUntil.Sub(now) < sidebarLeaseTTL-sidebarLeaseRenewEvery:
		if err := a.history.RenewSidebarLease(ctx, a.observationScope(), a.sidebarGeneration, leaseMillis(leaseUntil)); err != nil {
			w.written = ""
			return err
		}
		w.leaseUntil = leaseUntil
	}
	w.publishBackgroundChanges(ctx, observations)
	return nil
}

// leaseMillis encodes an unavailable source's zero lease as 0.
func leaseMillis(until time.Time) int64 {
	if until.IsZero() {
		return 0
	}
	return until.UnixMilli()
}
