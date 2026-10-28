package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/statev2"
)

func TestProjectsAndPluginDirectoryReadStoredHistoryNotNativeAdapters(t *testing.T) {
	s, native, id := identityHTTPFixture(t)
	native.sessions = []db.Session{{ID: "native-only", Directory: "/native", MessageCount: 99}}
	history := s.historyAnalytics.Store
	if err := history.RegisterInstance(t.Context(), statev2.Instance{ID: "stored", OwnerID: "owner", Platform: "test", SourceKey: "stored-source"}); err != nil {
		t.Fatal(err)
	}
	if _, err := history.Commit(t.Context(), statev2.Batch{Session: statev2.Session{Key: statev2.Key{InstanceID: "stored", SessionID: id}, Directory: "/stored/worktree", ProjectID: "project", Title: "stored", UpdatedAt: 50, Extensions: json.RawMessage(`{"ProjectDirectory":"/stored"}`)}, Messages: []statev2.Message{{ID: "message", Role: "assistant", TokensInput: 7, TokensOutput: 3, Cost: 2, Extensions: json.RawMessage(`{"role":"assistant","tokens":{"input":7,"output":3},"cost":2}`)}}}); err != nil {
		t.Fatal(err)
	}
	if rows, err := s.getProjects(t.Context()); err != nil || len(rows) != 1 || rows[0].Directory != "/stored/worktree" {
		t.Fatalf("incomplete import blocked committed projects: %+v %v", rows, err)
	}
	s.historyAnalytics.Instances = func() ([]string, bool) { return []string{"stored"}, true }
	projects, err := s.getProjects(t.Context())
	if err != nil || len(projects) != 1 || projects[0].Directory != "/stored/worktree" || projects[0].MessageCount != 1 || projects[0].TotalTokensIn != 7 {
		t.Fatalf("projects bypassed stored history: %+v %v", projects, err)
	}
	lookup := s.projectDirectory()
	if lookup == nil {
		t.Fatal("stored project directory lookup missing without native DB")
	}
	if dir, err := lookup(t.Context(), "project"); err != nil || dir != "/stored" {
		t.Fatalf("plugin project lookup bypassed history: %q %v", dir, err)
	}
}

func TestProjectIndexInvalidatesAfterStoredIngestionWithoutNativeEvents(t *testing.T) {
	s, _ := newSessionsTestServer(t)
	history := storedProjectFixture(t, s)
	s.projects.enrich = func(context.Context, []db.ProjectStats) error { return nil }
	add := func(id string) {
		t.Helper()
		if _, err := history.Commit(t.Context(), statev2.Batch{Session: statev2.Session{Key: statev2.Key{InstanceID: "fixture", SessionID: id}, Directory: "/stored"}}); err != nil {
			t.Fatal(err)
		}
	}
	add("first")
	if rows, err := s.hostProjects(t.Context()); err != nil || len(rows) != 1 || rows[0].SessionCount != 1 {
		t.Fatalf("initial projects: %+v %v", rows, err)
	}
	add("second")
	for deadline := time.Now().Add(time.Second); ; {
		rows, err := s.hostProjects(t.Context())
		if err == nil && len(rows) == 1 && rows[0].SessionCount == 2 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("stored ingestion did not invalidate project index: %+v %v", rows, err)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestMCPTextSearchReadsStoredHistoryWithoutNativeDatabase(t *testing.T) {
	s, _, id := identityHTTPFixture(t)
	history := s.historyAnalytics.Store
	if err := history.RegisterInstance(t.Context(), statev2.Instance{ID: "search", OwnerID: "owner", Platform: "test", SourceKey: "search"}); err != nil {
		t.Fatal(err)
	}
	if _, err := history.Commit(t.Context(), statev2.Batch{Session: statev2.Session{Key: statev2.Key{InstanceID: "search", SessionID: id}, Directory: "/stored", UpdatedAt: 100}, Messages: []statev2.Message{{ID: "m", Role: "user"}}, Parts: []statev2.Part{{ID: "p", MessageID: "m", Kind: "text", Text: "stored searchable text"}}}); err != nil {
		t.Fatal(err)
	}
	matches, err := (sessionMCPService{s}).SearchSessionText(t.Context(), "searchable", "/stored", 100)
	if err != nil || len(matches) != 1 || matches[0].SessionID != id || matches[0].Platform != "test" || matches[0].PartID != "p" {
		t.Fatalf("search bypassed history: %+v %v", matches, err)
	}
}
