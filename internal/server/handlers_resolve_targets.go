package server

import (
	"net/http"
	"strings"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/gitexec"
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

	origin := localGitOrigin(r, req.Dir)
	localIdents := s.localProjectIdentities(r)
	candidates := s.remotes.ResolveTargets(req.Dir, origin, localIdents)
	// Log the resolution so a mis-targeted launch (e.g. a remote path the
	// hub can't stat, yielding a basename-only identity that matches
	// nothing) is diagnosable. A remote dir with origin="" here means the
	// path doesn't exist on the hub — the caller should pass remoteId
	// directly instead of round-tripping through the resolver.
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

// localProjectIdentities builds origin-enriched ProjectIdentity records
// for the local machine's known projects, used to match the requested
// project against the local checkout.
func (s *Server) localProjectIdentities(r *http.Request) []remote.ProjectIdentity {
	projects, err := s.router().Local().Projects(r.Context())
	if err != nil {
		return nil
	}
	out := make([]remote.ProjectIdentity, 0, len(projects))
	for _, p := range projects {
		origin := localGitOrigin(r, p.Directory)
		out = append(out, remote.ProjectIdentity{
			Key:    remote.NormalizeProjectIdentity(origin, p.Directory),
			Origin: origin,
			Dir:    p.Directory,
		})
	}
	return out
}

// localGitOrigin returns the git origin URL for a local directory, or ""
// when the dir has no origin / isn't a repo.
func localGitOrigin(r *http.Request, dir string) string {
	// Deliberately local: the caller is localProjectIdentities, which
	// enumerates *this* machine's checkouts so ResolveTargets can offer
	// the hub as a candidate. Routing it through a Host would ask the
	// wrong machine.
	out, err := gitexec.Output(r.Context(), dir, "remote", "get-url", "origin") // ocman:allow-host-helper
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}
