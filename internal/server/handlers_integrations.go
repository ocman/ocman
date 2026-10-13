package server

import (
	"net/http"

	"github.com/NoUseFreak/ocman/internal/forge"
	"github.com/NoUseFreak/ocman/internal/forge/forgejo"
	"github.com/NoUseFreak/ocman/internal/forge/github"
)

// forgeClients holds the configured forge clients. Initialised once at
// server construction; the server exposes them under
// /api/integrations/<id>/ and the PR/Issue sidebar endpoints.
type forgeClients struct {
	GitHub  *github.Client
	Forgejo *forgejo.Registry
}

func newForgeClients() *forgeClients {
	return &forgeClients{
		GitHub:  github.New(),
		Forgejo: forgejo.NewRegistry(),
	}
}

// resolveForge returns the hub's client for a classified remote, or false
// when its type is unsupported or its host has no configured client.
func (s *Server) resolveForge(rem forge.Remote) (forge.Forge, bool) {
	if s.integrations == nil {
		return nil, false
	}
	switch rem.Type {
	case forge.RemoteTypeGitHub:
		if s.integrations.GitHub == nil {
			return nil, false
		}
		return s.integrations.GitHub, true
	case forge.RemoteTypeForgejo:
		if s.integrations.Forgejo == nil {
			return nil, false
		}
		c := s.integrations.Forgejo.ForHost(rem.Host)
		if c == nil {
			return nil, false
		}
		return c, true
	}
	return nil, false
}

// knowsForgejo reports whether the hub has a Forgejo client for host; it
// classifies an owner's remotes.
func (s *Server) knowsForgejo(host string) bool {
	return s.integrations != nil && s.integrations.Forgejo != nil && s.integrations.Forgejo.Knows(host)
}

// ---------------------------------------------------------------------------
// Integration status endpoint
// ---------------------------------------------------------------------------

// handleIntegrationsStatus returns which integrations are available and
// whether they are authenticated. For Forgejo it also reports the list of
// configured hosts so the frontend knows which hostnames to scan for
// previewable links (GitHub's host is fixed; Forgejo's are dynamic).
//
// GET /api/integrations/status
func (s *Server) handleIntegrationsStatus(w http.ResponseWriter, r *http.Request) {
	var forgejoHosts []string
	if s.integrations != nil && s.integrations.Forgejo != nil {
		forgejoHosts = s.integrations.Forgejo.Hosts()
	}
	if forgejoHosts == nil {
		forgejoHosts = []string{}
	}
	writeJSON(w, map[string]interface{}{
		"github": map[string]interface{}{
			"available":     true,
			"authenticated": s.integrations.GitHub.Authenticated(),
		},
		"forgejo": map[string]interface{}{
			"available": len(forgejoHosts) > 0,
			"hosts":     forgejoHosts,
		},
	})
}
