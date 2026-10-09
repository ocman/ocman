package server

import (
	"net/http"
	"strconv"
)

func (s *Server) handleProjectPRMergeability(w http.ResponseWriter, r *http.Request) {
	number, err := strconv.Atoi(r.URL.Query().Get("number"))
	if err != nil || number <= 0 {
		http.Error(w, "positive PR number is required", http.StatusBadRequest)
		return
	}
	_, remote, _, ok := s.parseProjectListParams(w, r)
	if !ok {
		return
	}
	f, ok := s.resolveForge(remote)
	if !ok {
		writeProjectListError(w, http.StatusUnauthorized, "auth_required", "no forge client configured for "+remote.Host)
		return
	}
	pr, err := f.LookupPR(r.Context(), remote.Repo, number)
	if err != nil {
		writeProjectListError(w, http.StatusBadGateway, "upstream_status", err.Error())
		return
	}
	approved, err := f.PRApproval(r.Context(), remote.Repo, number)
	if err != nil {
		writeProjectForgeError(w, r, remote, err)
		return
	}
	writeJSON(w, map[string]any{"mergeable": pr.Mergeable, "approved": approved})
}
