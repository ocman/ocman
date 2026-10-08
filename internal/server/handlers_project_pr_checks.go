package server

import (
	"errors"
	"net/http"
	"strings"

	"github.com/NoUseFreak/ocman/internal/forge"
	"github.com/NoUseFreak/ocman/internal/git"
)

// handleProjectPRChecks returns CI status for a PR's head commit.
// GET /api/project/pr-checks?dir=<abs>&remote=<name>&remoteId=<owner>&sha=<headSha>
func (s *Server) handleProjectPRChecks(w http.ResponseWriter, r *http.Request) {
	dir, ok := parseAbsDir(w, r)
	if !ok {
		return
	}
	remoteName := strings.TrimSpace(r.URL.Query().Get("remote"))
	sha := strings.TrimSpace(r.URL.Query().Get("sha"))
	if remoteName == "" || sha == "" {
		http.Error(w, "remote and sha query parameters are required", http.StatusBadRequest)
		return
	}
	if !validCommitSHA(sha) {
		http.Error(w, "sha must be a hexadecimal commit ID", http.StatusBadRequest)
		return
	}
	remoteID, ok := requireProjectRemoteID(w, r.URL.Query().Get("remoteId"))
	if !ok {
		return
	}
	host, ok := s.resolveOwner(w, dir, remoteID)
	if !ok {
		return
	}
	_, remotes, err := s.detectUpstreams(r.Context(), host, dir)
	if err != nil {
		if writeCancellation(w, "failed to detect upstreams", err) {
			return
		}
		if errors.Is(err, git.ErrNotARepo) {
			http.Error(w, "directory is not a git repository", http.StatusNotFound)
			return
		}
		http.Error(w, "failed to detect upstreams", http.StatusBadGateway)
		return
	}
	rem, ok := findRemote(remotes, remoteName)
	if !ok {
		http.Error(w, "remote not found among project upstreams", http.StatusNotFound)
		return
	}
	f, ok := s.resolveForge(rem)
	if !ok {
		writeProjectListError(w, http.StatusUnauthorized, "auth_required", "no forge client configured for "+rem.Host)
		return
	}
	ci, rl, err := f.Checks(r.Context(), rem.Repo, sha)
	if err != nil {
		writeProjectForgeError(w, r, rem, err)
		return
	}
	if writeProjectRateLimit(w, rl) {
		return
	}
	if ci.Checks == nil {
		ci.Checks = []forge.Check{}
	}
	writeJSON(w, map[string]interface{}{"state": ci.State, "checks": ci.Checks, "rateLimit": rl})
}
