package server

import (
	"net/http"
	"strings"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/remote"
)

// handleResolveTargets implements POST /api/sessions/resolve-targets — the
// hub-side machine picker resolver (AD-15). Given a project dir, it
// computes the project identity (AD-9), matches it against the local +
// remote project inventories, and returns the candidate machines:
//
//   - 1 candidate  -> the frontend auto-selects it
//   - >1 candidates -> the frontend prompts the operator to choose
//   - 0 candidates  -> the frontend shows the enabled remotes to pick from
//
// The response is { candidates: [...], remotes: [...] } where `remotes`
// is every enabled remote (for the zero-match path).
func (s *Server) handleResolveTargets(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Dir      string `json:"dir"`
		RemoteID string `json:"remoteId"`
	}
	if !readAndUnmarshal(w, r, maxRequestBody, &req) {
		return
	}
	req.Dir = strings.TrimSpace(req.Dir)
	if req.Dir == "" {
		http.Error(w, "dir is required", http.StatusBadRequest)
		return
	}
	owner, ok := s.resolveOwner(w, req.Dir, req.RemoteID)
	if !ok {
		return
	}

	// With no remote manager (single-host), the only candidate is local.
	if s.remotes == nil {
		writeJSON(w, map[string]any{
			"candidates": []remote.TargetCandidate{{
				RemoteID:   "local",
				RemoteName: "This machine",
				Platform:   "opencode",
				Dir:        req.Dir,
			}},
			"remotes": []remote.TargetCandidate{},
		})
		return
	}

	var origin string
	var keys []string
	project, err := owner.ProjectUpstreams(r.Context(), req.Dir)
	if err != nil && req.RemoteID != "" && req.RemoteID != "local" {
		http.Error(w, "Could not read the source project's git remotes", http.StatusBadGateway)
		return
	}
	if err == nil && project != nil {
		// Older owners send only the credential-free origin identity.
		origin = project.Identity
		keys = project.UpstreamKeys
	}
	if len(keys) == 0 && origin != "" {
		keys = []string{origin}
	}
	candidates := []remote.TargetCandidate{}
	if projects, err := s.router().Local().Projects(r.Context()); err == nil {
		local := make([]remote.ProjectIdentity, 0, len(projects))
		for _, p := range projects {
			local = append(local, remote.ProjectIdentity{Dir: p.Directory, UpstreamKeys: p.UpstreamKeys})
		}
		candidates = s.remotes.ResolveProjectTargets(db.ProjectStats{Directory: req.Dir, RemoteID: req.RemoteID, UpstreamKeys: keys}, local)
	}
	log.WithFields(log.Fields{
		"dir":        req.Dir,
		"origin":     origin,
		"candidates": len(candidates),
	}).Info("resolve-targets")
	writeJSON(w, map[string]any{
		"candidates": candidates,
		"remotes":    s.remotes.EnabledRemotes(),
	})
}
