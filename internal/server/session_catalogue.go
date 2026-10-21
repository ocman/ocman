package server

import (
	"github.com/NoUseFreak/ocman/internal/platforms/owned"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
	"github.com/NoUseFreak/ocman/internal/statev2"
	log "github.com/sirupsen/logrus"
)

func (s *Server) newSessionCatalogue(catalogue *sessioncatalog.Store, history *statev2.DB, ownerID string) *sessioncatalog.Module {
	return owned.NewLegacyCatalogue(catalogue, history, ownerID, s.stateDB, s.registry.Platforms, func(err error) { log.WithError(err).Warn("session catalogue metadata indexing failed; will retry") })
}
