package server

import (
	"github.com/ocman/ocman/internal/autoarchive"
	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platforms"
)

// autoArchiveModule is stateless, so it is rebuilt on each call. Its worker
// starts with the other session consumers, after identity cutover.
func (s *Server) autoArchiveModule() *autoarchive.Module {
	return autoarchive.NewModule(autoarchive.Deps{
		Store:         s.stateDB,
		Platforms:     func() []platforms.Platform { return s.registry.Platforms() },
		Local:         func() hostsvc.Host { return s.router().Local() },
		StoredHistory: s.hasStoredHistory,
		SessionReady: func(id string) bool {
			remoteID, _ := platforms.SplitPlatformID(id)
			return remoteID != "" || s.historyIngest == nil || s.historyIngest.InitialImportReady(id)
		},
	})
}
