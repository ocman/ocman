package server

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/owned"
	"github.com/NoUseFreak/ocman/internal/sessionrefrecovery"
	log "github.com/sirupsen/logrus"
)

func (s *Server) sessionRecoveryModule() *sessionrefrecovery.Module {
	if s.stateDB == nil || s.registry == nil || !s.registry.CanonicalSessionIDs() {
		return nil
	}
	return s.sessionRecoveryMod.get(func() *sessionrefrecovery.Module {
		references, err := owned.NewReferences(s.stateDB)
		if err != nil {
			return nil
		}
		return sessionrefrecovery.New(sessionrefrecovery.Deps{
			Scopes: func() []string {
				var scopes []string
				for _, p := range s.registry.Platforms() {
					if owner, _ := platforms.SplitPlatformID(string(p.ID())); owner != "" && owned.CanonicalSessionIDs(p) {
						scopes = append(scopes, string(p.ID()))
					}
				}
				return scopes
			},
			Resolve: func(ctx context.Context, scope string) (int, error) {
				if s.sessions == nil {
					return 0, platforms.ErrHistoryUnavailable
				}
				return s.sessions.RecoverSessionReferences(ctx, references, scope)
			},
			RequestReload: s.platformSettingsModule().RequestRecoveryReload,
			OnError: func(scope string, err error) {
				log.WithError(err).WithField("platform", scope).Warn("session reference owner recovery will retry")
			},
		})
	})
}
