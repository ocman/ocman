package server

import "github.com/ocman/ocman/internal/usagecollection"

func (s *Server) usageCollectionModule() *usagecollection.Module {
	return s.usageCollectionMod.get(func() *usagecollection.Module {
		return usagecollection.NewModule(usagecollection.Deps{
			Store:    s.stateDB,
			RelayURL: func() string { return s.relayURL },
			Version:  func() string { return s.version },
		})
	})
}
