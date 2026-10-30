package codexprovider

import (
	"encoding/json"
	"errors"
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
	var detail platforms.SessionDetail
	if code := h.call("GET", "/sessions/thr", nil, &detail); code != 200 || detail.Session.Title != "New" || len(detail.Messages) != 0 {
		t.Fatalf("%d %+v", code, detail)
	}
	for _, path := range []string{"/sessions/unknown", "/sessions/unknown/lifecycle"} {
		if code := h.call("GET", path, nil, nil); code != 404 {
			t.Fatal(path, code)
		}
	}
	var life platforms.SessionLifecycle
	if code := h.call("GET", "/sessions/thr/lifecycle", nil, &life); code != 200 || life.Status != db.StatusDone {
		t.Fatalf("%d %+v", code, life)
	}
	var list []db.Session
	h.call("GET", "/sessions?directory=/repo&since=1000", nil, &list)
	if len(list) != 2 || list[1].Status != db.StatusInterrupted {
		t.Fatal(list)
	}
	h.call("GET", "/sessions?since=3000", nil, &list)
	if len(list) != 0 {
		t.Fatal(list)
	}
	if code := h.call("GET", "/unknown", nil, nil); code != 501 {
		t.Fatal(code)
	}
	h.fake().Handle(codexappserver.MethodThreadList, func(json.RawMessage) (any, error) { return nil, errors.New("down") })
	if code := h.call("GET", "/sessions", nil, nil); code != 500 {
		t.Fatal(code)
	}
}

func TestNativeCatalogRoutes(t *testing.T) {
	h := newHarness(t, scripted)
	_, _, err := h.b.conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	h.fake().Handle(codexappserver.MethodModelList, func(json.RawMessage) (any, error) {
		return map[string]any{"data": []any{
			map[string]any{"id": "a", "model": "gpt-a", "displayName": "GPT A", "isDefault": true, "supportedReasoningEfforts": []any{map[string]any{"reasoningEffort": "high"}}},
			map[string]any{"id": "b", "model": "hidden", "hidden": true}, map[string]any{"id": "c", "model": "gpt-c"},
		}}, nil
	})
	h.fake().Handle(codexappserver.MethodSkillsList, func(json.RawMessage) (any, error) {
		return map[string]any{"data": []any{map[string]any{"cwd": "/repo", "skills": []any{
			map[string]any{"name": "pdf", "description": "long", "shortDescription": "PDF tools", "enabled": true},
			map[string]any{"name": "off", "enabled": false}, map[string]any{"name": "review", "enabled": true}, map[string]any{"name": "plain", "description": "fallback", "enabled": true},
		}}}}, nil
	})
	var catalog platforms.DirectoryCatalog
	if code := h.call("GET", "/catalog?directory=/repo", nil, &catalog); code != 200 {
		t.Fatal(code)
	}
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
	h.fake().Handle(codexappserver.MethodModelList, func(json.RawMessage) (any, error) { return nil, errors.New("down") })
	if code := h.call("GET", "/catalog", nil, nil); code != 500 {
		t.Fatal(code)
	}
}
