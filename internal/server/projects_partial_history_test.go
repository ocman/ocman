package server

import (
	"testing"

	"github.com/ocman/ocman/internal/conversationingest"
	"github.com/ocman/ocman/internal/statev2"
)

func TestProjectBrowsingDoesNotRequireAnalyticsReadiness(t *testing.T) {
	s := withReadyTestHistory(t, testServer(t))
	s.historyIngest = conversationingest.NewModule(conversationingest.Deps{})
	s.historyAnalytics.Instances = func() ([]string, bool) { return nil, false }
	if _, err := s.historyAnalytics.Store.Commit(t.Context(), statev2.Batch{Session: statev2.Session{
		Key: statev2.Key{InstanceID: "fixture", SessionID: "imported"}, Directory: "/imported", UpdatedAt: 1,
	}}); err != nil {
		t.Fatal(err)
	}
	if projects, err := s.storedProjects(t.Context()); err != nil || len(projects) != 1 || projects[0].Directory != "/imported" {
		t.Fatalf("project browsing blocked by incomplete source import: %+v %v", projects, err)
	}
	if _, ready := s.historyAnalytics.Instances(); ready {
		t.Fatal("project read incorrectly enabled complete-history analytics")
	}
}
