package server

import (
	"testing"

	"github.com/NoUseFreak/ocman/internal/analytics"
	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/statev2"
)

// Cache/archiving tests exercise scheduling and overlays with an injected
// reference query. Production's stored project source has separate tests.
func installProjectCacheFixture(t *testing.T, s *Server, reference *db.DB) {
	t.Helper()
	history, err := statev2.Open(t.TempDir() + "/history.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = history.Close() })
	s.historyAnalytics = &analytics.HistoryAnalytics{Store: history}
	s.historyOwner = "fixture"
	s.projects.fetch = func() ([]db.ProjectStats, error) { return reference.GetProjects(t.Context()) }
}

func storedProjectFixture(t *testing.T, s *Server) *statev2.DB {
	t.Helper()
	history, err := statev2.Open(t.TempDir() + "/history.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = history.Close() })
	s.historyAnalytics = &analytics.HistoryAnalytics{Store: history}
	s.historyOwner = "fixture"
	if err := history.RegisterInstance(t.Context(), statev2.Instance{ID: "fixture", OwnerID: "fixture", Platform: "test", SourceKey: "fixture"}); err != nil {
		t.Fatal(err)
	}
	return history
}
