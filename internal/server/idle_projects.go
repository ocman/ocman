package server

import (
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/idleprojects"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

func (s *Server) idleProjectsModule() *idleprojects.Module {
	s.idleProjectsOnce.Do(func() {
		if s.stateDB != nil {
			s.idleProjects = idleprojects.New(idleprojects.Deps{Store: s.stateDB, Targets: s.idleProjectTargets})
		}
	})
	return s.idleProjects
}

func (s *Server) idleProjectTargets() []idleprojects.Target {
	if s.registry == nil {
		return nil
	}
	var targets []idleprojects.Target
	if platform, ok := s.registry.Get(platforms.OpenCodeID); ok {
		if runtime, ok := s.router().Local().(hostsvc.IdleProjectRuntime); ok {
			targets = append(targets, idleprojects.Target{Platform: platform, Runtime: runtime})
		}
	}
	for _, platform := range s.registry.Platforms() {
		if runtime, ok := platform.(hostsvc.IdleProjectRuntime); ok {
			targets = append(targets, idleprojects.Target{Platform: platform, Runtime: runtime})
		}
	}
	return targets
}
