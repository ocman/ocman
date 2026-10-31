package server

import (
	"context"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/remote"
)

func TestProjectIndexGroupsRetainedPluginSourceWithOriginalProject(t *testing.T) {
	s, _ := newSessionsTestServer(t)
	// No source directory or Git metadata remains. Attribution is durable state.
	if _, err := s.stateDB.SQL().ExecContext(t.Context(), `INSERT INTO plugin_session_project
		(directory,plugin_id,project_directory) VALUES (?,?,?)`, "/private/source-2059222746", "reader", "/projects/platform-service"); err != nil {
		t.Fatal(err)
	}
	s.projects.fetch = func() ([]db.ProjectStats, error) {
		return []db.ProjectStats{{Directory: "/private/source-2059222746", SessionCount: 2, TotalTokensIn: 10, LastUsed: 123}}, nil
	}
	s.projects.enrich = func(_ context.Context, projects []db.ProjectStats) error {
		for i := range projects {
			if projects[i].Directory == "/projects/platform-service" {
				projects[i].UpstreamKeys = []string{"github.com/aspect-analytics/platform-service"}
			}
		}
		return nil
	}
	if err := s.refreshProjectsIndex(t.Context()); err != nil {
		t.Fatal(err)
	}
	projects, err := s.listProjects(t.Context())
	if err != nil || len(projects) != 2 {
		t.Fatalf("project inventory: %+v %v", projects, err)
	}
	if projects[0].AssociatedDirectory != "/projects/platform-service" || projects[0].ProjectKey != projects[1].ProjectKey || projects[0].ProjectKey != "git:github.com/aspect-analytics/platform-service" {
		t.Fatalf("source copy became a separate project: %+v", projects)
	}
	if projects[0].Directory != "/private/source-2059222746" || projects[0].SessionCount != 2 || projects[0].TotalTokensIn != 10 || projects[1].SessionCount != 0 {
		t.Fatalf("association moved checkout or duplicated totals: %+v", projects)
	}
	projects = append(projects, db.ProjectStats{Directory: "/remote/clone", RemoteID: "box", UpstreamKeys: projects[1].UpstreamKeys})
	remote.AssignProjectKeys(projects)
	if projects[0].ProjectKey != projects[2].ProjectKey {
		t.Fatal("association did not participate in upstream grouping")
	}
}
