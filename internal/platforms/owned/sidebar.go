package owned

import (
	"context"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/module"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/sidebar"
	"github.com/NoUseFreak/ocman/internal/statev2"
	"github.com/google/uuid"
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

type sidebarWorker struct{ adapter *Adapter }

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
	ticker := time.NewTicker(500 * time.Millisecond)
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
	for id, live := range observer.CachedSessionObservations() {
		observations[id] = statev2.SidebarObservation{Status: live.Status, ResetsAt: live.ResetsAt, PendingPermission: live.PendingPermission, PendingQuestion: live.PendingQuestion}
	}
	expiresAt := time.Now().Add(3 * time.Second).UnixMilli()
	if source, ok := a.native.(platforms.CachedSessionAvailability); ok && !source.CachedSessionAvailable() {
		expiresAt = 0
		observations = nil
	}
	return a.history.ReplaceSidebarObservations(ctx, a.observationScope(), a.sidebarGeneration, expiresAt, observations)
}
