package opencode

import (
	"context"
	"net/http"
	"testing"

	"github.com/NoUseFreak/ocman/internal/ocv2"
)

// On OpenCode v2 an empty small_model sends no model: the title agent picks
// one itself. Through the ocv2 translation that means the naming session's
// model is never switched (no POST /api/session/{id}/model).
func TestWorktreeName_V2EmptySmallModelSendsNoModel(t *testing.T) {
	defer ocv2.SetInstalledV2(true)()
	f := newV2Fake(t, true, func(w http.ResponseWriter, r *http.Request) bool {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/config":
			writeJSONBody(w, `[{"type":"document","info":{}}]`)
		case "POST /api/session":
			writeJSONBody(w, `{"data":{"id":"naming"}}`)
		case "GET /api/session/naming":
			writeJSONBody(w, `{"data":{"id":"naming","agent":"build"}}`)
		case "POST /api/session/naming/prompt":
			writeJSONBody(w, `{"data":{"id":"msg_0"}}`)
		case "GET /api/session/active":
			writeJSONBody(w, `{"data":{}}`)
		case "POST /api/session/naming/agent", "POST /api/session/naming/model",
			"POST /api/experimental/session/naming/wait",
			"POST /api/session/naming/interrupt", "DELETE /api/session/naming":
			writeJSONBody(w, `{}`)
		case "GET /api/session/naming/message":
			writeJSONBody(w, `{"data":[{"id":"msg_a","type":"assistant","time":{},"content":[{"type":"text","text":"Fix Login Flow"}]}]}`)
		default:
			return false
		}
		return true
	})
	got, err := WorktreeName(context.Background(), f.Port(), "/repo", "fix the login flow")
	if err != nil || got != "fix-login-flow" {
		t.Fatalf("WorktreeName = %q, %v; want fix-login-flow", got, err)
	}
	if _, hit := f.find(http.MethodPost, "/api/session/naming/model"); hit {
		t.Fatal("v2 naming switched the model although small_model is empty")
	}
	agent, ok := f.find(http.MethodPost, "/api/session/naming/agent")
	if !ok || agent.body["agent"] != "title" {
		t.Fatalf("title agent not selected: %+v", agent)
	}
	prompt, ok := f.find(http.MethodPost, "/api/session/naming/prompt")
	if !ok || prompt.body["text"] != "fix the login flow" {
		t.Fatalf("prompt = %+v", prompt)
	}
	if _, hit := f.find(http.MethodDelete, "/api/session/naming"); !hit {
		t.Fatal("temporary naming session leaked")
	}
}

// A v1 server keeps the haiku fallback even when v2 is installed.
func TestWorktreeName_V1ServerKeepsHaikuFallback(t *testing.T) {
	defer ocv2.SetInstalledV2(true)()
	f := newV2Fake(t, false, func(w http.ResponseWriter, r *http.Request) bool {
		switch r.Method + " " + r.URL.Path {
		case "GET /config":
			writeJSONBody(w, `{}`)
		case "POST /session":
			writeJSONBody(w, `{"id":"naming"}`)
		case "POST /session/naming/message":
			writeJSONBody(w, `{"parts":[{"type":"text","text":"Add Tests"}]}`)
		case "POST /session/naming/abort", "DELETE /session/naming":
			w.WriteHeader(http.StatusNoContent)
		default:
			return false
		}
		return true
	})
	got, err := WorktreeName(context.Background(), f.Port(), "/repo", "add tests")
	if err != nil || got != "add-tests" {
		t.Fatalf("WorktreeName = %q, %v; want add-tests", got, err)
	}
	msg, ok := f.find(http.MethodPost, "/session/naming/message")
	if !ok {
		t.Fatal("naming prompt not sent")
	}
	model, _ := msg.body["model"].(map[string]any)
	if model["providerID"] != "anthropic" || model["modelID"] != "claude-haiku-4-5" {
		t.Fatalf("model = %#v, want haiku fallback", msg.body["model"])
	}
}
