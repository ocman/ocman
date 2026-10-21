package owned

import (
	"context"
	"fmt"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
	"github.com/NoUseFreak/ocman/internal/startup"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/statev2"
)

// PrepareCore inventories provenance, rekeys owner-local history, quarantines
// unproven associations and returns canonical adapters. It must run before any
// readers, workers or event sinks use session state. Each durable step is
// replayable. The caller installs the returned adapters and enables canonical
// registry enforcement only after all feature-specific migrations have finished.
// It never connects to remotes or allocates identities on their behalf.
func PrepareCore(ctx context.Context, core *state.DB, history *statev2.DB, catalogue *sessioncatalog.Store, owner string, native []platforms.Integration) ([]platforms.Platform, error) {
	if core == nil || history == nil || catalogue == nil || owner == "" {
		return nil, sessioncatalog.ErrInvalid
	}
	adapters := make([]platforms.Platform, 0, len(native))
	for _, p := range native {
		if p == nil {
			return nil, sessioncatalog.ErrInvalid
		}
		if remote, _ := platforms.SplitPlatformID(string(p.ID())); remote != "" {
			return nil, fmt.Errorf("canonical core preparation requires owner-local adapters")
		}
		if CanonicalSessionIDs(p) {
			return nil, fmt.Errorf("canonical core preparation requires native adapters")
		}
		a, err := New(ctx, p, catalogue, history, owner)
		if err != nil {
			if p.Available(ctx) {
				return nil, err
			}
			a = newAdapter(p, catalogue, history, owner, platforms.HistoryIdentity{Platform: string(p.ID())}, nil, err)
		}
		adapters = append(adapters, a)
	}
	// Freeze legacy routine/Factory provenance before core state is rekeyed.
	startup.Report(ctx, "Inventorying session provenance", 0, 0)
	legacy := NewLegacyCatalogue(catalogue, history, owner, core, func() []platforms.Platform { return adapters }, nil)
	if err := legacy.Start(ctx); err != nil {
		return nil, err
	}
	routines, err := core.RoutineSessions(ctx)
	if err != nil {
		return nil, err
	}
	factory, err := core.FactorySessions(ctx)
	if err != nil {
		return nil, err
	}
	for _, p := range adapters {
		if p.(*Adapter).unavailable != nil {
			continue
		}
		if err := p.(*Adapter).inventoryLegacy(ctx, routines, factory); err != nil {
			return nil, err
		}
	}
	startup.Report(ctx, "Migrating retained conversation history", 0, 0)
	if err := MigrateHistory(ctx, catalogue, history, owner); err != nil {
		return nil, err
	}
	for _, p := range adapters {
		a := p.(*Adapter)
		if a.unavailable != nil {
			continue
		}
		if err := catalogue.RetainSourceLegacyKeys(ctx, sessioncatalog.Source{OwnerID: owner, Platform: a.source.Platform, SourceKey: a.source.SourceKey}); err != nil {
			return nil, err
		}
	}
	references, err := NewReferences(core)
	if err != nil {
		return nil, err
	}
	scopes, err := core.SessionReferenceScopes(ctx)
	if err != nil {
		return nil, err
	}
	for _, scope := range scopes {
		startup.Report(ctx, "Restoring owner-local session references", 0, 0)
		if err := references.QuarantineState(ctx, core, scope); err != nil {
			return nil, err
		}
		if scope == "" {
			continue
		}
		if remote, _ := platforms.SplitPlatformID(scope); remote != "" {
			continue
		}
		if err := references.restoreLocalState(ctx, core, catalogue, owner, scope); err != nil {
			return nil, err
		}
	}
	return adapters, ctx.Err()
}

func (a *Adapter) inventoryLegacy(ctx context.Context, routines, factory map[state.Key]string) error {
	startup.Report(ctx, "Reading "+string(a.ID())+" session inventory", 0, 0)
	// Identity migration needs scalar metadata, never transcript aggregates or
	// live status. Reuse the uncached runtime identity inventory when available.
	var rows []db.Session
	var err error
	if reader, ok := a.native.(platforms.RuntimeSessionReader); ok {
		rows, err = reader.GetSessionIdentities(ctx)
	} else {
		rows, err = a.native.Sessions(ctx, "", 0)
	}
	if err != nil {
		return err
	}
	metadata := make([]sessioncatalog.NativeSession, 0, len(rows))
	for _, row := range rows {
		native := sessioncatalog.NativeSession{ID: row.ID, ParentID: row.ParentID, Directory: row.Directory, Title: row.Title, CreatedAt: row.TimeCreated}
		key := state.Key{Platform: string(a.ID()), SessionID: row.ID}
		if id := routines[key]; id != "" {
			native.Origin, native.OriginID = sessioncatalog.Routine, id
		} else if id := factory[key]; id != "" {
			native.Origin, native.OriginID = sessioncatalog.Factory, id
		}
		metadata = append(metadata, native)
	}
	if err := a.checkSource(ctx); err != nil {
		return err
	}
	if _, err := a.store.Reconcile(ctx, sessioncatalog.Source{OwnerID: a.owner, Platform: a.source.Platform, SourceKey: a.source.SourceKey}, metadata); err != nil {
		return err
	}
	return nil
}
