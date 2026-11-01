package server

import (
	"net/http"
	"sort"
	"strings"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/httpx"
	"github.com/ocman/ocman/internal/state"
)

// Checkout membership stays private. HTTP clients receive only final identities.
type projectIdentity struct {
	Key        string `json:"key"`
	Directory  string `json:"directory"`
	RemoteID   string `json:"remoteId,omitempty"`
	RemoteName string `json:"remoteName,omitempty"`
	Platform   string `json:"platform,omitempty"`
}

type projectCheckout struct {
	Directory string `json:"directory"`
	RemoteID  string `json:"remoteId,omitempty"`
}

type projectCatalog struct {
	projects  []db.ProjectStats
	checkouts map[string]projectIdentity
}

func finalProjectCatalog(checkouts []db.ProjectStats) projectCatalog {
	representatives := make(map[string]db.ProjectStats)
	byCheckout := make(map[string]db.ProjectStats, len(checkouts))
	for _, p := range checkouts {
		byCheckout[state.ProjectMergeKey(p.RemoteID, p.Directory)] = p
		previous, ok := representatives[p.ProjectKey]
		if !ok || (state.NormalizeRemoteID(previous.RemoteID) != state.LocalRemoteID && state.NormalizeRemoteID(p.RemoteID) == state.LocalRemoteID) {
			representative := p
			if p.AssociatedDirectory != "" {
				representative.Directory = p.AssociatedDirectory
			}
			representatives[p.ProjectKey] = representative
		}
	}
	for _, p := range checkouts {
		if p.MergedIntoDirectory == "" {
			continue
		}
		target := byCheckout[state.ProjectMergeKey(p.MergedIntoRemoteID, p.MergedIntoDirectory)]
		target.Directory, target.RemoteID, target.ProjectKey = p.MergedIntoDirectory, p.MergedIntoRemoteID, p.ProjectKey
		representatives[p.ProjectKey] = target
	}
	groups := make(map[string]*db.ProjectStats, len(representatives))
	index := make(map[string]projectIdentity, len(checkouts))
	for _, p := range checkouts {
		target := representatives[p.ProjectKey]
		identity := projectIdentity{Key: p.ProjectKey, Directory: target.Directory, RemoteID: target.RemoteID, RemoteName: target.RemoteName, Platform: target.Platform}
		index[state.ProjectMergeKey(p.RemoteID, p.Directory)] = identity
		index[state.ProjectMergeKey(target.RemoteID, target.Directory)] = identity
		group, ok := groups[p.ProjectKey]
		if !ok {
			group = &db.ProjectStats{ProjectKey: p.ProjectKey, Directory: target.Directory, RemoteID: target.RemoteID, RemoteName: target.RemoteName, Platform: target.Platform, UpstreamKeys: target.UpstreamKeys, UpstreamOrigin: target.UpstreamOrigin, Archived: true}
			groups[p.ProjectKey] = group
		}
		group.SessionCount += p.SessionCount
		group.MessageCount += p.MessageCount
		group.TotalTokensIn += p.TotalTokensIn
		group.TotalTokensOut += p.TotalTokensOut
		group.TotalCost += p.TotalCost
		group.LastUsed = max(group.LastUsed, p.LastUsed)
		group.Archived = group.Archived && p.Archived
	}
	projects := make([]db.ProjectStats, 0, len(groups))
	for _, group := range groups {
		projects = append(projects, *group)
	}
	sort.Slice(projects, func(i, j int) bool {
		if projects[i].LastUsed != projects[j].LastUsed {
			return projects[i].LastUsed > projects[j].LastUsed
		}
		return projects[i].ProjectKey < projects[j].ProjectKey
	})
	return projectCatalog{projects: projects, checkouts: index}
}

func (c projectCatalog) resolve(checkout projectCheckout) projectIdentity {
	root := projectRootForDirectory(checkout.Directory)
	key := state.ProjectMergeKey(checkout.RemoteID, root)
	if project, ok := c.checkouts[key]; ok {
		return project
	}
	return projectIdentity{Key: key, Directory: root, RemoteID: checkout.RemoteID}
}

func (s *Server) handleResolveProjectIdentities(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Checkouts []projectCheckout `json:"checkouts"`
	}
	if !httpx.ReadJSON(w, r, httpx.MaxRequestBody, &req) {
		return
	}
	for _, checkout := range req.Checkouts {
		if strings.TrimSpace(checkout.Directory) == "" {
			http.Error(w, "directory is required", http.StatusBadRequest)
			return
		}
	}
	projects, err := s.listProjects(r.Context())
	if err != nil {
		httpx.ServerError(w, "resolving projects", err)
		return
	}
	catalog := finalProjectCatalog(projects)
	identities := make([]projectIdentity, 0, len(req.Checkouts))
	for _, checkout := range req.Checkouts {
		identities = append(identities, catalog.resolve(checkout))
	}
	httpx.WriteJSON(w, identities)
}
