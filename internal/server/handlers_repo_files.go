package server

import (
	"errors"
	"net/http"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/git"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
)

// repoFilesOwner parses `dir` + `remoteId` and resolves the owning host.
func (s *Server) repoFilesOwner(w http.ResponseWriter, r *http.Request) (string, hostsvc.Host, bool) {
	dir, ok := parseAbsDir(w, r)
	if !ok {
		return "", nil, false
	}
	remoteID, ok := requireProjectRemoteID(w, r.URL.Query().Get("remoteId"))
	if !ok {
		return "", nil, false
	}
	host, ok := s.resolveOwner(w, dir, remoteID)
	return dir, host, ok
}

func writeRepoFilesError(w http.ResponseWriter, err error, what string) {
	if errors.Is(err, git.ErrNotARepo) || errors.Is(err, git.ErrFileNotFound) {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	log.WithError(err).Warn(what + " failed")
	http.Error(w, what+" failed", http.StatusBadGateway)
}

// handleRepoFiles lists every non-ignored file of the repository
// containing `dir`, relative to its root. Backs the Explore modal.
//
//	GET /api/git/files?dir=<abs>&remoteId=<id>
//	200 {"root": "/repo", "files": ["a.go", ...], "truncated": false}
//	404 not a repository
func (s *Server) handleRepoFiles(w http.ResponseWriter, r *http.Request) {
	dir, host, ok := s.repoFilesOwner(w, r)
	if !ok {
		return
	}
	list, err := host.ListRepoFiles(r.Context(), dir)
	if err != nil {
		writeRepoFilesError(w, err, "list files")
		return
	}
	writeJSON(w, list)
}

// handleRepoFile returns one listed file (`path`, root-relative) of the
// repository containing `dir`. Ignored or unlisted paths are 404.
//
//	GET /api/git/file?dir=<abs>&path=<rel>&remoteId=<id>
//	200 {"path": "a.go", "content": "...", "size": 12, "binary": false, "truncated": false}
func (s *Server) handleRepoFile(w http.ResponseWriter, r *http.Request) {
	dir, host, ok := s.repoFilesOwner(w, r)
	if !ok {
		return
	}
	path := r.URL.Query().Get("path")
	if path == "" {
		http.Error(w, "path query parameter is required", http.StatusBadRequest)
		return
	}
	file, err := host.ReadRepoFile(r.Context(), dir, path)
	if err != nil {
		writeRepoFilesError(w, err, "read file")
		return
	}
	writeJSON(w, file)
}
