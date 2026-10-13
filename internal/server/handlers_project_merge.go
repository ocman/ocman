package server

import (
	"context"
	"errors"
	"net/http"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/state"
)

func (s *Server) applyProjectMerges(ctx context.Context, projects []db.ProjectStats) error {
	merges, err := s.stateDB.ProjectMerges(ctx)
	if err != nil {
		return err
	}
	for i, p := range projects {
		target := merges.Resolve(state.ProjectMergeTarget{Key: p.ProjectKey})
		if target.Key != p.ProjectKey {
			projects[i].ProjectKey = target.Key
			projects[i].MergedIntoDirectory = target.Directory
			projects[i].MergedIntoRemoteID = target.RemoteID
		}
	}
	return nil
}

// Merge is hub-local display bookkeeping; sessions retain their owning checkout.
func (s *Server) handleProjectMerge(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Source state.ProjectMergeTarget `json:"source"`
		Target state.ProjectMergeTarget `json:"target"`
	}
	if !readAndUnmarshal(w, r, maxRequestBody, &req) {
		return
	}
	projects, err := s.listProjects(r.Context())
	if err != nil {
		serverError(w, "fetching projects", err)
		return
	}
	find := func(target state.ProjectMergeTarget) (state.ProjectMergeTarget, bool) {
		for _, p := range projects {
			if p.Directory == target.Directory && state.NormalizeRemoteID(p.RemoteID) == state.NormalizeRemoteID(target.RemoteID) {
				return state.ProjectMergeTarget{Key: p.ProjectKey, Directory: p.Directory, RemoteID: p.RemoteID}, true
			}
		}
		return state.ProjectMergeTarget{}, false
	}
	source, sourceOK := find(req.Source)
	target, targetOK := find(req.Target)
	if !sourceOK || !targetOK {
		http.Error(w, "select two known projects", http.StatusBadRequest)
		return
	}
	err = s.stateDB.MergeProjects(r.Context(), source.Key, target)
	if errors.Is(err, state.ErrSameProject) {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err != nil {
		serverError(w, "merging projects", err)
		return
	}
	s.broadcastGlobalEvent("ocman.projects.changed", []byte(`{}`))
	writeJSON(w, map[string]bool{"ok": true})
}
