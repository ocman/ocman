package codexprovider

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	codexappserver "github.com/ocman/ocman/internal/platforms/codex/appserver"
	"github.com/ocman/ocman/internal/platforms/codex/appserver/codextest"
)

func thread(id string) map[string]any {
	return map[string]any{"id": id, "cwd": "/repo", "source": "appServer", "status": map[string]any{"type": "idle"},
		"createdAt": 1, "updatedAt": 2, "modelProvider": "openai", "preview": "hello", "turns": []any{}}
}

// Only native reads and title metadata are implemented. Execution requests
// deliberately have no fake handler, so tests cannot hide a native fallback.
func scripted(s *codextest.Server) {
	s.Handle(codexappserver.MethodThreadNameSet, func(json.RawMessage) (any, error) { return map[string]any{}, nil })
	s.Handle(codexappserver.MethodThreadList, func(json.RawMessage) (any, error) {
		active := thread("thr")
		active["status"] = map[string]any{"type": "active"}
		return map[string]any{"data": []any{thread("old"), active, map[string]any{"id": "eph", "ephemeral": true, "source": "cli", "status": map[string]any{"type": "idle"}}}}, nil
	})
	s.Handle(codexappserver.MethodThreadRead, func(json.RawMessage) (any, error) {
		return nil, &codexappserver.RPCError{Code: -32600, Message: "thread not loaded: x"}
	})
}

func TestNativeReadRoutes(t *testing.T) {
	h := newHarness(t, scripted)
	h.b.mu.Lock()
	h.b.sessionLocked("thr").dir = "/repo"
	h.b.sessions["thr"].title = "New"
	h.b.mu.Unlock()
	if code := h.call("GET", "/sessions/unknown/lifecycle", nil, nil); code != 404 {
		t.Fatal(code)
	}
	var life platforms.SessionLifecycle
	if code := h.call("GET", "/sessions/thr/lifecycle", nil, &life); code != 200 || life.Status != db.StatusDone {
		t.Fatalf("%d %+v", code, life)
	}
	// Browsing reads come from the history app-server, never the supervisor.
	for _, path := range []string{"/sessions", "/sessions?directory=/repo", "/sessions/thr", "/unknown"} {
		if code := h.call("GET", path, nil, nil); code != 501 {
			t.Fatal(path, code)
		}
	}
}

func TestCatalogAndCommands(t *testing.T) {
	effort := []codexappserver.ReasoningEffort{{ReasoningEffort: "high"}}
	catalog := Catalog([]codexappserver.Model{
		{ID: "a", Model: "gpt-a", DisplayName: "GPT A", IsDefault: true, SupportedReasoningEfforts: effort},
		{ID: "b", Model: "hidden", Hidden: true}, {ID: "c", Model: "gpt-c"},
	})
	catalog.Commands = Commands([]codexappserver.SkillsEntry{{Cwd: "/repo", Skills: []codexappserver.Skill{
		{Name: "pdf", Description: "long", ShortDescription: "PDF tools", Enabled: true},
		{Name: "off"}, {Name: "review", Enabled: true}, {Name: "plain", Description: "fallback", Enabled: true},
	}}})
	var names []string
	for _, cmd := range catalog.Commands {
		names = append(names, cmd.Name)
	}
	if !slices.Equal(names, []string{"review", "compact", "pdf", "plain"}) || catalog.Commands[3].Description != "fallback" {
		t.Fatal(catalog.Commands)
	}
	m := catalog.Models
	if len(m.Models) != 2 || m.Models[0].ModelName != "GPT A" || m.Models[0].Reasoning[0] != "high" || m.Models[1].ModelName != "gpt-c" || m.ProviderDefaults["openai"] != "gpt-a" {
		t.Fatalf("%+v", m)
	}
}
