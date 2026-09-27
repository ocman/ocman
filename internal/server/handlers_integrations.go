package server

import (
	"net/http"

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
