package remote

import (
	"strings"

	"github.com/NoUseFreak/ocman/internal/db"
)

// AssignProjectKeys labels connected components of shared upstreams. Checkout
// rows stay separate so their owner, directory, stats and archive state survive.
func AssignProjectKeys(projects []db.ProjectStats) {
	parent := make([]int, len(projects))
	var root func(int) int
	root = func(i int) int {
		if parent[i] != i {
			parent[i] = root(parent[i])
		}
		return parent[i]
	}
	seen := map[string]int{}
	for i, p := range projects {
		parent[i] = i
		for _, key := range p.UpstreamKeys {
			if key == "" {
				continue
			}
			if previous, ok := seen[key]; ok {
				parent[root(i)] = root(previous)
			} else {
				seen[key] = i
			}
		}
	}
	keys := map[int]string{}
	for i, p := range projects {
		for _, key := range p.UpstreamKeys {
			if key != "" && (keys[root(i)] == "" || key < keys[root(i)]) {
				keys[root(i)] = key
			}
		}
	}
	for i := range projects {
		if key := keys[root(i)]; key != "" {
			projects[i].ProjectKey = "git:" + key
		} else {
			owner := projects[i].RemoteID
			if owner == "" {
				owner = "local"
			}
			projects[i].ProjectKey = "dir:" + owner + ":" + projects[i].Directory
		}
	}
}

// Older owners advertise only the origin key. Basenames never match across hosts.
func identityUpstreams(p ProjectIdentity) []string {
	if len(p.UpstreamKeys) != 0 {
		return p.UpstreamKeys
	}
	if p.Key != "" && !strings.HasPrefix(p.Key, "basename:") {
		return []string{p.Key}
	}
	return nil
}

// ResolveProjectTargets shares the project list's common-upstream components.
// One checkout per machine is offered; an exact source checkout wins locally.
func (m *Manager) ResolveProjectTargets(source db.ProjectStats, localProjects []ProjectIdentity) []TargetCandidate {
	projects := []db.ProjectStats{source}
	for _, p := range localProjects {
		projects = append(projects, db.ProjectStats{Directory: p.Dir, RemoteID: "local", UpstreamKeys: identityUpstreams(p)})
	}
	m.invMu.RLock()
	for owner, inventory := range m.inventory {
		for _, p := range inventory {
			projects = append(projects, db.ProjectStats{Directory: p.Dir, RemoteID: owner, UpstreamKeys: identityUpstreams(p)})
		}
	}
	m.invMu.RUnlock()
	AssignProjectKeys(projects)
	var out []TargetCandidate
	seen := map[string]int{}
	for _, p := range projects[1:] {
		if p.ProjectKey != projects[0].ProjectKey {
			continue
		}
		if i, ok := seen[p.RemoteID]; ok {
			if p.Directory == source.Directory && (p.RemoteID == source.RemoteID || (p.RemoteID == "local" && source.RemoteID == "")) {
				out[i].Dir = p.Directory
			}
			continue
		}
		candidate := TargetCandidate{RemoteID: p.RemoteID, Dir: p.Directory, Platform: m.base, RemoteName: "This machine"}
		if p.RemoteID != "local" {
			candidate.Platform = CompoundPlatformID(p.RemoteID, m.base)
			candidate.RemoteName = m.nameForRemoteID(p.RemoteID)
		}
		seen[p.RemoteID] = len(out)
		out = append(out, candidate)
	}
	return out
}
