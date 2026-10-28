package owned

import (
	"context"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/sessioncatalog"
	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/statev2"
)

// NewLegacyCatalogue supports inventories written before canonical ingestion.
// Its native provenance attribution runs only for the selected physical source.
// Canonical ingestion adopts metadata directly and does not install this worker.
func NewLegacyCatalogue(catalogue *sessioncatalog.Store, history *statev2.DB, ownerID string, core *state.DB, adapters func() []platforms.Platform) *sessioncatalog.Module {
	var routines, factory map[state.Key]string
	active := map[sessioncatalog.Source]bool{}
	return sessioncatalog.NewModule(sessioncatalog.Deps{Store: catalogue, History: history, OwnerID: ownerID, Prepare: func(ctx context.Context) error {
		clear(active)
		var err error
		routines, err = core.RoutineSessions(ctx)
		if err != nil {
			return err
		}
		factory, err = core.FactorySessions(ctx)
		if err != nil {
			return err
		}
		for _, adapter := range adapters() {
			if remote, _ := platforms.SplitPlatformID(string(adapter.ID())); remote != "" {
				continue
			}
			reader, ok := adapter.(platforms.HistoryReader)
			if !ok {
				continue
			}
			identity, err := reader.HistoryIdentity(ctx)
			if err == nil {
				active[sessioncatalog.Source{OwnerID: ownerID, Platform: identity.Platform, SourceKey: identity.SourceKey}] = true
			}
		}
		return nil
	}, Classify: func(source sessioncatalog.Source, row *sessioncatalog.NativeSession) {
		if !active[source] {
			return
		}
		key := state.Key{Platform: source.Platform, SessionID: row.ID}
		if id := routines[key]; id != "" {
			row.Origin, row.OriginID = sessioncatalog.Routine, id
		} else if id := factory[key]; id != "" {
			row.Origin, row.OriginID = sessioncatalog.Factory, id
		}
	}})
}
