package server

import (
	"github.com/ocman/ocman/internal/forge"
	"github.com/ocman/ocman/internal/sourcecontrol"
)

// discoverForgeClients reads this machine's forge credentials when a Server
// is built. Tests replace it so they never read the developer's gh token or
// tea logins. projectforge and factory reach the clients through
// resolveForge; the previews module sees only their credentials.
var discoverForgeClients = sourcecontrol.DiscoverClients

// resolveForge returns the hub's client for a classified remote, or false
// when its type is unsupported or its host has no configured client.
func (s *Server) resolveForge(rem forge.Remote) (forge.Forge, bool) {
	// GitHub operations always target the canonical API, never a caller-supplied host.
	if rem.Type == forge.RemoteTypeGitHub {
		rem.Host = "github.com"
	}
	ctx, cancel := s.lifetimeCall()
	defer cancel()
	return s.sourceControlModule().Resolve(ctx, rem)
}

// knowsForgejo reports whether the hub has a Forgejo client for host; it
// classifies an owner's remotes.
func (s *Server) knowsForgejo(host string) bool {
	return s.sourceControlModule().Knows(host)
}
