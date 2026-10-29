package server

import (
	"context"

	"github.com/ocman/ocman/internal/forge"
	"github.com/ocman/ocman/internal/forge/forgejo"
	"github.com/ocman/ocman/internal/forge/github"
)

// forgeClients holds the hub's forge clients, initialised once at server
// construction. projectforge and factory reach them through resolveForge;
// the previews module sees only their credentials (previewForges).
type forgeClients struct {
	GitHub  *github.Client
	Forgejo *forgejo.Registry
}

// discoverForgeClients reads this machine's forge credentials when a Server
// is built. Tests replace it so they never read the developer's gh token or
// tea logins.
var discoverForgeClients = newForgeClients

func newForgeClients() *forgeClients {
	return &forgeClients{
		GitHub:  github.New(),
		Forgejo: forgejo.NewRegistry(),
	}
}

// resolveForge returns the hub's client for a classified remote, or false
// when its type is unsupported or its host has no configured client.
func (s *Server) resolveForge(rem forge.Remote) (forge.Forge, bool) {
	// GitHub operations always target the canonical API, never a caller-supplied host.
	if rem.Type == forge.RemoteTypeGitHub {
		rem.Host = "github.com"
	}
	return s.sourceControlModule().Resolve(context.Background(), rem)
}

// knowsForgejo reports whether the hub has a Forgejo client for host; it
// classifies an owner's remotes.
func (s *Server) knowsForgejo(host string) bool {
	return s.sourceControlModule().Knows(host)
}
