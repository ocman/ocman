package server

import (
	"encoding/json"
	"sort"
	"testing"

	"github.com/ocman/ocman/internal/analytics"
	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/statev2"
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
	s.projects.fetch = func() ([]db.ProjectStats, error) { return fixtureProjects(t, reference) }
}

// Cache tests use mutable native fixtures, not the production analytics API.
func fixtureProjects(t *testing.T, reference *db.DB) ([]db.ProjectStats, error) {
	entries, err := reference.HistoryInventory(t.Context())
	if err != nil {
		return nil, err
	}
	projects := map[string]*db.ProjectStats{}
	for _, entry := range entries {
		snapshot, err := reference.HistorySnapshot(t.Context(), entry.ID)
		if err != nil {
			return nil, err
		}
		s := snapshot.Session
		p := projects[s.Directory]
		if p == nil {
			p = &db.ProjectStats{Directory: s.Directory}
			projects[s.Directory] = p
		}
		if !snapshot.Subagent {
			p.SessionCount++
		}
		p.LastUsed = max(p.LastUsed, s.TimeUpdated)
		p.MessageCount += len(snapshot.Messages)
		for _, m := range snapshot.Messages {
			var md db.MessageData
			if err := json.Unmarshal(m.Data, &md); err != nil {
				return nil, err
			}
			if md.Role == "assistant" {
				p.TotalCost += md.Cost
				if md.Tokens != nil {
					p.TotalTokensIn += md.Tokens.Input
					p.TotalTokensOut += md.Tokens.Output
				}
			}
		}
	}
	var out []db.ProjectStats
	for _, p := range projects {
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastUsed > out[j].LastUsed })
	return out, nil
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
