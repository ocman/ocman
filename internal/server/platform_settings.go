package server

import (
	"context"
	"github.com/ocman/ocman/internal/hostsvc"
	"strings"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/identity"
	"github.com/ocman/ocman/internal/platformsettings"
)

func (s *Server) platformSettingsModule() *platformsettings.Module {
	return s.platformSettingsMod.get(func() *platformsettings.Module {
		return platformsettings.New(s.stateDB).WithSetup(func() hostsvc.PlatformSetupManager {
			host, _ := s.router().Local().(hostsvc.PlatformSetupManager)
			return host
		})
	})
}

func (s *Server) WithConfigReload(reload func(), selected string, overridden bool) *Server {
	s.platformSettingsModule().Configure(reload, strings.Split(selected, ","), overridden, s.configIdle)
	return s
}

func (s *Server) beginConfigTurn(ctx context.Context, id platforms.ID) (func(), error) {
	if isRemotePlatformID(string(id)) {
		return func() {}, nil
	}
	return s.platformSettingsModule().Begin(ctx)
}

func (s *Server) prepareQueuedSend(ctx context.Context, id string, req platforms.SendMessageRequest) (func(context.Context) error, error) {
	send, err := s.prepareSendNow(ctx, id, req)
	if err != nil {
		return nil, err
	}
	return func(delivery context.Context) error { return send(platformsettings.DrainContext(delivery)) }, nil
}

// Reload the complete backend composition only after its local work settles.
// Reads fail closed: an unavailable status never means it is safe to detach.
func (s *Server) configIdle(ctx context.Context) (bool, error) {
	queued, err := s.stateDB.SessionsWithQueuedMessages(ctx)
	if err != nil {
		return false, err
	}
	for _, q := range queued {
		if identity.IsPendingReference(q.SessionID) {
			continue
		}
		if !isRemotePlatformID(q.Platform) {
			return false, nil
		}
	}
	for _, p := range s.registry.Platforms() {
		if isRemotePlatformID(string(p.ID())) {
			continue
		}
		if reader, ok := p.(platforms.ReloadIdleReader); ok {
			idle, err := reader.ReloadIdle(ctx)
			if err != nil || !idle {
				return false, err
			}
			continue
		}
		sessions, err := p.Sessions(ctx, "", 0)
		if err != nil {
			return false, err
		}
		for _, session := range sessions {
			if platforms.ReloadSessionActive(session) {
				return false, nil
			}
		}
	}
	return true, nil
}
