package server

import (
	"net/http"
	"slices"
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

	origin := ""
	matchDir := req.Dir
	if req.RemoteID != "" && req.RemoteID != "local" {
		project, err := owner.ProjectUpstreams(r.Context(), req.Dir)
		if err != nil {
			http.Error(w, "Could not read the source project's git remotes", http.StatusBadGateway)
			return
		}
		for _, upstream := range project.Remotes {
			if upstream.Name == "origin" {
				origin = upstream.URL
				break
			}
		}
		// Identical paths on different hosts do not identify the same project.
		matchDir = ""
	} else {
		origin = localGitOrigin(r, req.Dir)
	}
	candidates := []remote.TargetCandidate{}
	if matchDir != "" || origin != "" {
		localIdents := s.localProjectMatch(r, matchDir, origin)
		candidates = s.remotes.ResolveTargets(req.Dir, origin, localIdents)
		if origin == "" {
			candidates = slices.DeleteFunc(candidates, func(c remote.TargetCandidate) bool { return c.RemoteID != "local" })
		}
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

// localProjectMatch returns the local project matching dir's
// identity (AD-9), or nil. ResolveTargets only uses the first local match,
// so this stops there: an exact directory match needs no git call, and
// otherwise origins are read one project at a time until one matches.
// ponytail: a dir with no local match still shells out once per project;
// cache origins by dir if resolving remote-only projects gets slow.
func (s *Server) localProjectMatch(r *http.Request, dir, origin string) []remote.ProjectIdentity {
	projects, err := s.router().Local().Projects(r.Context())
	if err != nil {
		return nil
	}
	key := remote.NormalizeProjectIdentity(origin, dir)
	for _, p := range projects {
		if p.Directory == dir {
			return []remote.ProjectIdentity{{Key: key, Origin: origin, Dir: dir}}
		}
	}
	for _, p := range projects {
		o := localGitOrigin(r, p.Directory)
		if k := remote.NormalizeProjectIdentity(o, p.Directory); k == key {
			return []remote.ProjectIdentity{{Key: k, Origin: o, Dir: p.Directory}}
		}
	}
	return nil
}

// localGitOrigin returns the git origin URL for a local directory, or ""
// when the dir has no origin / isn't a repo.
func localGitOrigin(r *http.Request, dir string) string {
	// Deliberately local: the caller is localProjectMatch, which
	// enumerates *this* machine's checkouts so ResolveTargets can offer
	// the hub as a candidate. Routing it through a Host would ask the
	// wrong machine.
	out, err := gitexec.Output(r.Context(), dir, "remote", "get-url", "origin") // ocman:allow-host-helper
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}
