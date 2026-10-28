package server

import (
	"github.com/ocman/ocman/internal/snooze"
)

// The module is stateless, so each lifecycle entry can use a fresh value.
func (s *Server) snoozeModule() *snooze.Module {
	return snooze.NewModule(snooze.Deps{
		Store:     s.stateDB,
		Validate:  s.validateStateRequest,
		Broadcast: s.broadcastGlobalEvent,
	})
}
