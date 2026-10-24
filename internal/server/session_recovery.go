package server

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/ocruntime"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/opencode/apiv2"
	"github.com/NoUseFreak/ocman/internal/platforms/opencode/provider"
	"github.com/NoUseFreak/ocman/internal/sessionrecovery"
)

// Runtime hooks resolve these dependencies when called, including after tests
// or startup replace the registry, database or local host.
func (s *Server) sessionRecovery() *sessionrecovery.Service {
	var sessions sessionrecovery.SessionReader
	if s.registry != nil {
		if adapter, ok := s.registry.Get(opencode.PlatformID); ok {
			sessions, _ = adapter.(platforms.RuntimeSessionReader)
		}
	}
	return sessionrecovery.New(sessionrecovery.Deps{
		State: s.stateDB, Sessions: sessions,
		Platform: func(id platforms.ID) (platforms.Platform, bool) { return s.registry.Get(id) },
		ManagedRootReader: func() hostsvc.ManagedRootReader {
			reader, _ := s.router().Local().(hostsvc.ManagedRootReader)
			return reader
		},
		// Runtime replacement fences OpenCode instances only.
		MachineWide: ocv2.InstalledV2, BroadcastSessionChanged: func(id string) { s.broadcastSessionChanged(string(opencode.PlatformID), id) },
		InjectHistoryNotice: injectHistoryNotice,
	})
}

func (s *Server) recordOpencodeReplacement(ctx context.Context, root, reason string) error {
	return s.sessionRecovery().RecordOpencodeReplacement(ctx, root, reason)
}

func (s *Server) beginOpencodeReplacementStopWithInstance(ctx context.Context, root string, inst *ocruntime.Instance) error {
	return s.sessionRecovery().BeginOpencodeReplacementStopWithInstance(ctx, root, inst)
}

func (s *Server) confirmOpencodeReplacement(ctx context.Context, root string) error {
	return s.sessionRecovery().ConfirmOpencodeReplacement(ctx, root)
}

func (s *Server) opencodeReplacementStopping(ctx context.Context, root string) (*ocruntime.Instance, error) {
	return s.sessionRecovery().OpencodeReplacementStopping(ctx, root)
}

func (s *Server) cancelOpencodeReplacementStop(ctx context.Context, root string) error {
	return s.sessionRecovery().CancelOpencodeReplacementStop(ctx, root)
}

func (s *Server) enrichInterruptionHistory(ctx context.Context, platform, sessionID string, detail *platforms.SessionDetail) {
	s.sessionRecovery().EnrichInterruptionHistory(ctx, platform, sessionID, detail)
}
