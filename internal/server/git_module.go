package server

import (
	"net/http"

	"github.com/NoUseFreak/ocman/internal/gitmod"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/module"
)

// gitModule is stateless, so it is rebuilt on each call. Permission
// inheritance and the first send stay the server's shared session paths.
func (s *Server) gitModule() *gitmod.Module {
	return s.newGitModule(module.Guards{Auth: s.requireAuth, Localhost: s.requireLocalhost})
}

func (s *Server) newGitModule(g module.Guards) *gitmod.Module {
	return gitmod.NewModule(gitmod.Deps{
		Owner:       s.resolveOwner,
		ForDir:      func(dir string) hostsvc.Host { return s.router().ForDir(dir) },
		PlatformFor: opencodePlatformForHost,
		InheritPermissions: func(r *http.Request, parentSessionID string) func(string) (int, string) {
			in := s.buildInheritedPermissions(r, platformHint(r), parentSessionID)
			return func(child string) (int, string) { return s.applyInheritedPermissions(r, in, child) }
		},
		SendNow: s.sendNow,
		Guards:  g,
	})
}
