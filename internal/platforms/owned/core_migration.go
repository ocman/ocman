package owned

import (
	"context"
	"fmt"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/sessioncatalog"
	"github.com/ocman/ocman/internal/startup"
	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/statev2"
)

// BuildCore constructs canonical adapters without enumerating historical data.
// Consumers must remain fenced until PrepareBuiltCore has completed.
func BuildCore(ctx context.Context, core *state.DB, history *statev2.DB, catalogue *sessioncatalog.Store, owner string, native []platforms.Integration) ([]platforms.Platform, error) {
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
	return adapters, nil
}

// PrepareBuiltCore performs replayable identity cutover with consumers stopped:
// it inventories provenance, rekeys owner-local history and quarantines
// unproven associations. The adapters are constructed before serving; this
// never replaces live ones, connects to remotes or allocates their identities.
func PrepareBuiltCore(ctx context.Context, core *state.DB, history *statev2.DB, catalogue *sessioncatalog.Store, owner string, adapters []platforms.Platform) error {
	ready, err := history.SessionRekeyInventoryReady(ctx, owner)
	if err != nil {
		return err
	}
	if !ready {
		if err := inventoryCore(ctx, core, history, catalogue, owner, adapters); err != nil {
			return err
		}
	}
	return migrateCore(ctx, core, history, catalogue, owner, adapters)
}

func inventoryCore(ctx context.Context, core *state.DB, history *statev2.DB, catalogue *sessioncatalog.Store, owner string, adapters []platforms.Platform) error {
	// Freeze legacy routine/Factory provenance before core state is rekeyed.
	startup.Report(ctx, "Inventorying session provenance", 0, 0)
	legacy := NewLegacyCatalogue(catalogue, history, owner, core, func() []platforms.Platform { return adapters })
	if err := legacy.Start(ctx); err != nil {
		return err
	}
	routines, err := core.RoutineSessions(ctx)
	if err != nil {
		return err
	}
	factory, err := core.FactorySessions(ctx)
	if err != nil {
		return err
	}
	for _, p := range adapters {
		if p.(*Adapter).unavailable != nil {
			continue
		}
		if err := p.(*Adapter).inventoryLegacy(ctx, routines, factory); err != nil {
			return err
		}
	}
	return nil
}

func migrateCore(ctx context.Context, core *state.DB, history *statev2.DB, catalogue *sessioncatalog.Store, owner string, adapters []platforms.Platform) error {
	startup.Report(ctx, "Migrating retained conversation history", 0, 0)
	if err := MigrateHistory(ctx, catalogue, history, owner); err != nil {
		return err
	}
	for _, p := range adapters {
		a := p.(*Adapter)
		if a.unavailable != nil {
			continue
		}
		if err := catalogue.RetainSourceLegacyKeys(ctx, sessioncatalog.Source{OwnerID: owner, Platform: a.source.Platform, SourceKey: a.source.SourceKey}); err != nil {
			return err
		}
	}
	references, err := NewReferences(core)
	if err != nil {
		return err
	}
	scopes, err := core.SessionReferenceScopes(ctx)
	if err != nil {
		return err
	}
	for _, scope := range scopes {
		startup.Report(ctx, "Restoring owner-local session references", 0, 0)
		if err := references.QuarantineState(ctx, core, scope); err != nil {
			return err
		}
		if scope == "" {
			continue
		}
		if remote, _ := platforms.SplitPlatformID(scope); remote != "" {
			continue
		}
		if err := references.restoreLocalState(ctx, core, catalogue, owner, scope); err != nil {
			return err
		}
	}
	return ctx.Err()
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
	for len(metadata) > 0 {
		n := min(len(metadata), 500)
		if _, err := a.store.Reconcile(ctx, sessioncatalog.Source{OwnerID: a.owner, Platform: a.source.Platform, SourceKey: a.source.SourceKey}, metadata[:n]); err != nil {
			return err
		}
		metadata = metadata[n:]
	}
	return nil
}
