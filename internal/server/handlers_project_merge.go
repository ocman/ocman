package server

import (
	"context"
	"errors"
	"net/http"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/httpx"
	"github.com/NoUseFreak/ocman/internal/state"
)

func (s *Server) applyProjectMerges(ctx context.Context, projects []db.ProjectStats) error {
	merges, err := s.stateDB.ProjectMerges(ctx)
	if err != nil {
		return err
	}
	currentKeys := make(map[string]string, len(projects))
	for _, p := range projects {
		currentKeys[state.ProjectMergeKey(p.RemoteID, p.Directory)] = p.ProjectKey
	}
	currentKey := func(checkout string) string {
		if key, ok := currentKeys[checkout]; ok {
			return key
		}
		return checkout
	}
	// Upstream changes can cross saved merge pairs. Treat both kinds of
	// membership as connected components, not directed group edges.
	parent := map[string]string{}
	var root func(string) string
	root = func(key string) string {
		if _, ok := parent[key]; !ok {
			parent[key] = key
		}
		if parent[key] != key {
			parent[key] = root(parent[key])
		}
		return parent[key]
	}
	for source, target := range merges {
		a, b := root(currentKey(source)), root(currentKey(target.Key))
		if a < b {
			parent[b] = a
		} else {
			parent[a] = b
		}
	}
	targets := state.ProjectMerges{}
	for _, saved := range merges {
		target := merges.Resolve(saved)
		component := root(currentKey(target.Key))
		previous, ok := targets[component]
		// Crossed groups can have multiple saved destinations. Stable checkout
		// order chooses the same representative regardless of inventory order.
		if !ok || target.Key < previous.Key {
			targets[component] = target
		}
	}
	for i, p := range projects {
		if target, ok := targets[root(p.ProjectKey)]; ok {
			projects[i].ProjectKey = currentKey(target.Key)
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
	if !httpx.ReadJSON(w, r, httpx.MaxRequestBody, &req) {
		return
	}
	projects, err := s.listProjects(r.Context())
	if err != nil {
		httpx.ServerError(w, "fetching projects", err)
		return
	}
	find := func(target state.ProjectMergeTarget) (state.ProjectMergeTarget, bool) {
		for _, p := range projects {
			if p.Directory == target.Directory && state.NormalizeRemoteID(p.RemoteID) == state.NormalizeRemoteID(target.RemoteID) {
				return state.ProjectMergeTarget{Key: p.ProjectKey, Directory: p.Directory, RemoteID: p.RemoteID}, true
			}
			if p.MergedIntoDirectory != "" && p.MergedIntoDirectory == target.Directory && state.NormalizeRemoteID(p.MergedIntoRemoteID) == state.NormalizeRemoteID(target.RemoteID) {
				return state.ProjectMergeTarget{Key: p.ProjectKey, Directory: p.MergedIntoDirectory, RemoteID: p.MergedIntoRemoteID}, true
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
	if source.Key == target.Key {
		http.Error(w, state.ErrSameProject.Error(), http.StatusBadRequest)
		return
	}
	sources := []string{}
	for _, p := range projects {
		if p.ProjectKey == source.Key {
			sources = append(sources, state.ProjectMergeKey(p.RemoteID, p.Directory))
		}
	}
	target.Key = state.ProjectMergeKey(target.RemoteID, target.Directory)
	err = s.stateDB.MergeProjects(r.Context(), sources, target)
	if errors.Is(err, state.ErrSameProject) {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err != nil {
		httpx.ServerError(w, "merging projects", err)
		return
	}
	s.broadcastGlobalEvent("ocman.projects.changed", []byte(`{}`))
	httpx.WriteJSON(w, map[string]bool{"ok": true})
}
