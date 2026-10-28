package server

import (
	"testing"
	"time"

	"github.com/ocman/ocman/internal/analytics"
	"github.com/ocman/ocman/internal/statev2/statev2test"
)

func TestConversationResetCannotResurrectCachedProjects(t *testing.T) {
	srv := testServer(t)
	if err := srv.historyAnalytics.Store.SaveProjectsCache(t.Context(), srv.historyOwner,
		[]byte(`[{"directory":"/old-project","sessionCount":2}]`), time.Now()); err != nil {
		t.Fatal(err)
	}
	srv.loadProjectsIndexCache(t.Context())
	if projects, loaded := srv.projectsSnapshot(); !loaded || len(projects) != 1 {
		t.Fatal("fixture did not populate the cache")
	}
	// A fresh conversation store after an explicit offline reset. The same
	// application database (and all settings/identities) remains in use.
	srv.historyAnalytics = &analytics.HistoryAnalytics{Store: statev2test.Open(t)}
	srv.projects = projectsIndexState{}
	srv.loadProjectsIndexCache(t.Context())
	if projects, loaded := srv.projectsSnapshot(); loaded || len(projects) != 0 {
		t.Fatalf("old projects resurrected from application state: %+v", projects)
	}
}
