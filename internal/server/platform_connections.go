package server

import (
	"context"
	"fmt"
	"github.com/ocman/ocman/internal/platforms"
	"net/http"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platformconnections"
	"github.com/ocman/ocman/internal/sessioncatalog"
)

// WithPlatformConnections activates named routing before the Host or modules
// are constructed. Inspection remains an owner-native probe, not Available.
func (s *Server) WithPlatformConnections(router *platformconnections.Router, store *platformconnections.Store, owner string, lifecycle hostsvc.ConnectionLifecycle, configs map[string]hostsvc.ConnectionConfiguration) *Server {
	s.connections, s.connectionStore = router, store
	if lifecycle.Activate == nil {
		lifecycle.Activate = func(context.Context, platformconnections.Connection) error {
			if !s.platformSettingsModule().RequestRecoveryReload() {
				return platformconnections.ErrUnavailable
			}
			return nil
		}
	}
	s.connectionLifecycle, s.connectionConfigurations = lifecycle, configs
	if router != nil && s.stateDB != nil {
		catalogue, err := sessioncatalog.New(s.stateDB)
		if err == nil {
			err = s.sessions.WithConnections(router, catalogue, owner)
		}
		if err != nil {
			panic(fmt.Sprintf("invalid named session composition: %v", err))
		}
	}
	return s
}

func (s *Server) connectionDefault(ctx context.Context) (string, error) {
	if s.connectionStore != nil {
		c, err := s.connectionStore.Default(ctx)
		return c.ID, err
	}
	return s.stateDB.DefaultPlatform(ctx)
}

func (s *Server) resolveConnectionOwner(r *http.Request) (hostsvc.Host, error) {
	owner := r.URL.Query().Get("remoteId")
	if owner == "local" {
		return s.router().Local(), nil
	}
	if owner == "" {
		return nil, platformconnections.ErrInvalid
	}
	h, ok := s.router().LookupRemote(owner)
	if !ok {
		return nil, platforms.ErrRemoteOffline
	}
	return h, nil
}
