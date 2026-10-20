package server

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/statev2"
	log "github.com/sirupsen/logrus"
)

func (s *Server) newSessionCatalogue(catalogue *sessioncatalog.Store, history *statev2.DB, ownerID string) *sessioncatalog.Module {
	var routines, factory map[state.Key]string
	active := map[sessioncatalog.Source]bool{}
	return sessioncatalog.NewModule(sessioncatalog.Deps{Store: catalogue, History: history, OwnerID: ownerID,
		Prepare: func(ctx context.Context) error {
			clear(active)
			var err error
			routines, err = s.stateDB.RoutineSessions(ctx)
			if err != nil {
				return err
			}
			factory, err = s.stateDB.FactorySessions(ctx)
			if err != nil {
				return err
			}
			for _, adapter := range s.registry.Platforms() {
				if remote, _ := platforms.SplitPlatformID(string(adapter.ID())); remote != "" {
					continue
				}
				reader, ok := adapter.(platforms.HistoryReader)
				if !ok {
					continue
				}
				// Unavailable sources still get identities from retained metadata,
				// but native-only legacy provenance is not guessed for them.
				identity, err := reader.HistoryIdentity(ctx)
				if err == nil {
					active[sessioncatalog.Source{OwnerID: ownerID, Platform: identity.Platform, SourceKey: identity.SourceKey}] = true
				}
			}
			return nil
		},
		Classify: func(source sessioncatalog.Source, row *sessioncatalog.NativeSession) {
			if !active[source] {
				return
			}
			key := state.Key{Platform: source.Platform, SessionID: row.ID}
			if id := routines[key]; id != "" {
				row.Origin, row.OriginID = sessioncatalog.Routine, id
			} else if id := factory[key]; id != "" {
				row.Origin, row.OriginID = sessioncatalog.Factory, id
			}
		},
		OnError: func(err error) { log.WithError(err).Warn("session catalogue metadata indexing failed; will retry") },
	})
}
