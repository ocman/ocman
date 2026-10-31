package remote

import (
	"context"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	pb "github.com/ocman/ocman/internal/remote/proto"
)

type associatedProjectHost struct{ localStubHost }

func (associatedProjectHost) Projects(context.Context) ([]db.ProjectStats, error) {
	return []db.ProjectStats{{Directory: "/private/source", AssociatedDirectory: "/projects/platform-service", SessionCount: 2}}, nil
}

func TestProjectAssociationSurvivesHostRPCRoundTrip(t *testing.T) {
	conn := startTestServer(t, "tok", NewServer(platforms.NewRegistry(), associatedProjectHost{}, "rid", "v"))
	host := newRemoteHost(&RemoteConn{client: pb.NewOcmanClient(conn), remoteID: "rid"})
	projects, err := host.Projects(t.Context())
	if err != nil || len(projects) != 1 || projects[0].AssociatedDirectory != "/projects/platform-service" || projects[0].Directory != "/private/source" || projects[0].SessionCount != 2 {
		t.Fatalf("RPC lost project association: %+v %v", projects, err)
	}
}

func TestAssociatedSourceSkipsGitDiscoveryAndClearsInheritedRemotes(t *testing.T) {
	previous := projectUpstreamsCache
	defer func() { projectUpstreamsCache = previous }()
	projectUpstreamsCache = newOriginCache()
	target := t.TempDir()
	reads := 0
	projectUpstreamsCache.read = func(_ context.Context, dir string) (string, error) {
		reads++
		if dir != target {
			t.Errorf("private source triggered Git discovery: %s", dir)
		}
		return "origin https://host/project.git (fetch)", nil
	}
	projects := []db.ProjectStats{
		{Directory: "/deleted/source", AssociatedDirectory: target, UpstreamKeys: []string{"unrelated/parent"}, UpstreamOrigin: "unrelated/parent"},
		{Directory: target},
	}
	if err := EnrichProjectStats(t.Context(), projects); err != nil {
		t.Fatal(err)
	}
	if reads != 1 || len(projects[0].UpstreamKeys) != 0 || projects[0].UpstreamOrigin != "" || len(projects[1].UpstreamKeys) != 1 {
		t.Fatalf("associated discovery: reads=%d projects=%+v", reads, projects)
	}
}

func TestAssociatedSourceCopiesGroupWithOwnerProject(t *testing.T) {
	projects := []db.ProjectStats{
		{Directory: "/private/source-2059222746", AssociatedDirectory: "/projects/platform-service", SessionCount: 1, UpstreamKeys: []string{"aaa/unrelated-parent"}},
		{Directory: "/projects/platform-service", UpstreamKeys: []string{"github.com/aspect-analytics/platform-service"}},
		{Directory: "/remote/clone", RemoteID: "box", UpstreamKeys: []string{"github.com/aspect-analytics/platform-service"}},
		{Directory: "/projects/platform-service", RemoteID: "unrelated"},
		{Directory: "/unrelated-parent", UpstreamKeys: []string{"aaa/unrelated-parent"}},
	}
	AssignProjectKeys(projects)
	for _, p := range projects[:3] {
		if p.ProjectKey != projects[1].ProjectKey {
			t.Fatalf("source copy became a separate project: %+v", projects)
		}
	}
	if projects[3].ProjectKey == projects[0].ProjectKey {
		t.Fatal("association crossed owner boundary")
	}
	if projects[4].ProjectKey == projects[0].ProjectKey {
		t.Fatal("prepared source inherited an unrelated parent repository")
	}
	if projects[0].Directory != "/private/source-2059222746" || projects[0].SessionCount != 1 {
		t.Fatal("association changed source checkout or statistics")
	}
}

func TestAssociationWithoutGitUsesTargetDirectoryIdentity(t *testing.T) {
	projects := []db.ProjectStats{
		{Directory: "/private/source", AssociatedDirectory: "/projects/non-git", RemoteID: "box"},
		{Directory: "/projects/non-git", RemoteID: "box"},
		{Directory: "/projects/non-git"},
	}
	AssignProjectKeys(projects)
	if projects[0].ProjectKey != "dir:box:/projects/non-git" || projects[0].ProjectKey != projects[1].ProjectKey || projects[2].ProjectKey == projects[0].ProjectKey {
		t.Fatalf("non-Git association identity: %+v", projects)
	}
}

func TestRemoteInventoryPreservesAssociationAndNeverOffersSourceAsTarget(t *testing.T) {
	m := newInvManager(t)
	stats := []db.ProjectStats{
		{Directory: "/private/source", AssociatedDirectory: "/remote/project", SessionCount: 2},
		{Directory: "/remote/project", UpstreamKeys: []string{"host/project"}},
	}
	m.inventory["box"] = projectIdentities(stats)
	projects := m.RemoteProjects()
	if len(projects) != 2 || projects[0].AssociatedDirectory != "/remote/project" || projects[0].RemoteID != "box" || projects[0].SessionCount != 2 {
		t.Fatalf("remote inventory lost attribution: %+v", projects)
	}
	AssignProjectKeys(projects)
	if projects[0].ProjectKey != projects[1].ProjectKey {
		t.Fatal("remote source copy was not grouped with remote project")
	}
	local := []ProjectIdentity{
		{Dir: "/private/local-source", AssociatedDirectory: "/local/project", UpstreamKeys: []string{"host/project"}},
		{Dir: "/local/project", UpstreamKeys: []string{"host/project"}},
	}
	targets := m.ResolveProjectTargets(db.ProjectStats{Directory: "/local/project", UpstreamKeys: []string{"host/project"}}, local)
	if len(targets) != 2 {
		t.Fatalf("project targets: %+v", targets)
	}
	for _, target := range targets {
		if target.Dir != "/local/project" && target.Dir != "/remote/project" {
			t.Fatalf("private source copy offered for session creation: %+v", target)
		}
	}
}
