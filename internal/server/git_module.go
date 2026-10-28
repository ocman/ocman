package server

import (
	"net/http"

	"github.com/ocman/ocman/internal/gitmod"
	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/httpx"
)

// gitModule is stateless, so it is rebuilt on each call. Permission
// inheritance and the first send stay the server's shared session paths.
func (s *Server) gitModule() *gitmod.Module {
	return gitmod.NewModule(gitmod.Deps{
		Owner:           s.resolveOwner,
		ForDir:          func(dir string) hostsvc.Host { return s.router().ForDir(dir) },
		ResolvePlatform: s.worktreePlatform,
		InheritPermissions: func(r *http.Request, platform, parentSessionID string) func(string) (int, string) {
			in := s.buildInheritedPermissions(r, platform, parentSessionID)
			return func(child string) (int, string) { return s.applyInheritedPermissions(r, in, child) }
		},
		SendNow:   s.sendNow,
		OnGitInfo: s.persistSidebarBranches,
	})
}

// worktreePlatform resolves a worktree session's platform. /wt from a
// session splits onto the parent's platform, not the default.
func (s *Server) worktreePlatform(r *http.Request, host hostsvc.Host, requested, model, parentSessionID string) (string, string, error) {
	if requested == "" {
		requested = httpx.PlatformHint(r)
	}
	if requested == "" && model == "" && parentSessionID != "" && s.registry != nil {
		if parent, ok := s.registry.PlatformForSession(r.Context(), parentSessionID); ok {
			requested = string(parent.ID())
		}
	}
	return s.createPlatform(r.Context(), host, requested, model)
}
