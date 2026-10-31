package remote

import (
	"context"
	"strings"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
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
		if p.AssociatedDirectory != "" {
			continue
		}
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
	checkouts := map[string]int{}
	for i, p := range projects {
		checkouts[projectCheckoutKey(p.RemoteID, p.Directory)] = i
	}
	for i, p := range projects {
		if p.AssociatedDirectory != "" {
			if target, ok := checkouts[projectCheckoutKey(p.RemoteID, p.AssociatedDirectory)]; ok {
				parent[root(i)] = root(target)
			}
		}
	}
	keys := map[int]string{}
	fallbacks := map[int]string{}
	for i, p := range projects {
		if p.AssociatedDirectory == "" {
			key := projectDirectoryKey(p.RemoteID, p.Directory)
			if previous := fallbacks[root(i)]; previous == "" || key < previous {
				fallbacks[root(i)] = key
			}
		}
		if p.AssociatedDirectory != "" {
			continue
		}
		for _, key := range p.UpstreamKeys {
			if key != "" && (keys[root(i)] == "" || key < keys[root(i)]) {
				keys[root(i)] = key
			}
		}
	}
	for i := range projects {
		if key := keys[root(i)]; key != "" {
			projects[i].ProjectKey = "git:" + key
		} else if key := fallbacks[root(i)]; key != "" {
			projects[i].ProjectKey = key
		} else {
			projects[i].ProjectKey = projectDirectoryKey(projects[i].RemoteID, projects[i].Directory)
		}
	}
}

func projectDirectoryKey(owner, directory string) string {
	if owner == "" {
		owner = "local"
	}
	return "dir:" + owner + ":" + directory
}

func projectCheckoutKey(owner, directory string) string {
	if owner == "" {
		owner = "local"
	}
	return owner + "\x00" + directory
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
		if p.AssociatedDirectory != "" {
			continue
		}
		projects = append(projects, db.ProjectStats{Directory: p.Dir, RemoteID: "local", UpstreamKeys: identityUpstreams(p)})
	}
	m.invMu.RLock()
	for owner, inventory := range m.inventory {
		for _, p := range inventory {
			if p.AssociatedDirectory != "" {
				continue
			}
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
		// The hub's own default among its enabled platforms, never a
		// disabled one (FR-1/2).
		candidate := TargetCandidate{RemoteID: p.RemoteID, Dir: p.Directory, Platform: m.localDefault(), RemoteName: "This machine"}
		if p.RemoteID != "local" {
			_, preferred := platforms.SplitPlatformID(source.Platform)
			if preferred == "" {
				preferred = m.base
			}
			candidate.Platform = platforms.CompoundPlatformID(p.RemoteID, selectPlatform(m.remotePlatforms(p.RemoteID), preferred))
			candidate.RemoteName = m.nameForRemoteID(p.RemoteID)
		}
		seen[p.RemoteID] = len(out)
		out = append(out, candidate)
	}
	return out
}

func (m *Manager) remotePlatforms(remoteID string) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, mr := range m.remotes {
		if mr.host != nil && mr.conn != nil && mr.conn.RemoteID() == remoteID {
			return mr.conn.Platforms()
		}
	}
	return []string{"opencode"}
}

// localDefault is the saved default platform when this machine enabled it,
// else one it did enable.
func (m *Manager) localDefault() string {
	preferred := m.base
	if m.store != nil {
		if saved, err := m.store.DefaultPlatform(context.Background()); err == nil && saved != "" {
			preferred = saved
		}
	}
	return selectPlatform(m.localPlatforms(), preferred)
}

// localPlatforms lists the platforms this machine registered itself.
func (m *Manager) localPlatforms() []string {
	var out []string
	if m.registry == nil {
		return out
	}
	for _, p := range m.registry.Platforms() {
		if owner, base := platforms.SplitPlatformID(string(p.ID())); owner == "" {
			out = append(out, base)
		}
	}
	return out
}

func selectPlatform(enabled []string, preferred string) string {
	if len(enabled) == 0 {
		return preferred
	}
	for _, base := range enabled {
		if base == preferred {
			return base
		}
	}
	return enabled[0]
}
