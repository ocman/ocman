package server

import (
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/idleprojects"
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
	for _, platform := range s.registry.Platforms() {
		if runtime, ok := platform.(hostsvc.IdleProjectRuntime); ok {
			targets = append(targets, idleprojects.Target{Platform: platform, Runtime: runtime})
		}
	}
	return targets
}
