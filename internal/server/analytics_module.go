package server

import (
	"github.com/NoUseFreak/ocman/internal/analytics"
	"github.com/NoUseFreak/ocman/internal/module"
)

// analyticsModule exists without either database: its routes then answer
// as they did before the extraction (501, 503 or an empty list).
func (s *Server) analyticsModule() *analytics.Module {
	s.analyticsModOnce.Do(func() {
		if s.analyticsMod == nil {
			s.analyticsMod = analytics.NewModule(analytics.Deps{
				DB:        s.db,
				Analytics: s.analytics,
				Store:     s.stateDB,
				Version:   func() string { return s.version },
				Guards:    module.Guards{Auth: s.requireAuth, Localhost: s.requireLocalhost},
			})
		}
	})
	return s.analyticsMod
}
