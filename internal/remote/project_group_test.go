package remote

import (
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
)

func TestAssignProjectKeys(t *testing.T) {
	projects := []db.ProjectStats{
		{Directory: "/a", UpstreamKeys: []string{"host/fork", "host/shared"}},
		{Directory: "/b", RemoteID: "other", UpstreamKeys: []string{"host/shared", "host/mirror"}},
		{Directory: "/c", UpstreamKeys: []string{"host/mirror"}},
		{Directory: "/a", RemoteID: "isolated"},
		{Directory: "/elsewhere/a"},
	}
	AssignProjectKeys(projects)
	for _, p := range projects[:3] {
		if p.ProjectKey != "git:host/fork" {
			t.Fatalf("shared upstream component: %+v", projects)
		}
	}
	if projects[3].ProjectKey == projects[0].ProjectKey || projects[3].ProjectKey == projects[4].ProjectKey {
		t.Fatal("unrelated directories/owners were merged")
	}
	AssignProjectKeys(nil)
}

func TestResolveProjectTargetsCommonUpstreams(t *testing.T) {
	m := newInvManager(t)
	m.inventory["remote"] = []ProjectIdentity{{Dir: "/remote/clone", UpstreamKeys: []string{"host/shared", "host/mirror"}}}
	m.inventory["third"] = []ProjectIdentity{{Dir: "/third/repo", UpstreamKeys: []string{"host/mirror"}}}
	local := []ProjectIdentity{{Dir: "/local/repo", UpstreamKeys: []string{"host/fork", "host/shared"}}}
	source := db.ProjectStats{Directory: "/local/repo", UpstreamKeys: []string{"host/fork"}}
	if got := m.ResolveProjectTargets(source, local); len(got) != 3 {
		t.Fatalf("transitive shared upstreams: %+v", got)
	}
	// Matching basenames or paths alone never prove cross-owner identity.
	m.inventory["unrelated"] = []ProjectIdentity{{Dir: "/local/repo", Key: "basename:repo"}}
	if got := m.ResolveProjectTargets(db.ProjectStats{Directory: "/local/repo"}, []ProjectIdentity{{Dir: "/local/repo"}}); len(got) != 1 || got[0].RemoteID != "local" {
		t.Fatalf("directory-only matching: %+v", got)
	}
	local = append(local, ProjectIdentity{Dir: "/local/other", UpstreamKeys: []string{"host/shared"}})
	source.Directory = "/local/other"
	for _, candidate := range m.ResolveProjectTargets(source, local) {
		if candidate.RemoteID == "local" && candidate.Dir != source.Directory {
			t.Fatalf("exact checkout lost: %+v", candidate)
		}
	}
	if got := identityUpstreams(ProjectIdentity{}); got != nil {
		t.Fatalf("empty identity = %v", got)
	}
}
