package opencode

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/platformtest"
	"github.com/ocman/ocman/internal/sessionsvc"
)

func TestConfiguredCreationAppliesPermissionsAndSelectionsInOneRequest(t *testing.T) {
	for _, rules := range []string{`[]`, `[{"permission":"*","pattern":"*","action":"allow"}]`} {
		t.Run(rules, func(t *testing.T) {
			const sid = "ses_create_fast"
			f := newV2Fake(t, true, func(w http.ResponseWriter, r *http.Request) bool {
				if r.Method == http.MethodPost && r.URL.Path == "/api/session" {
					writeJSONBody(w, `{"data":{"id":"`+sid+`","agent":"plan","model":{"providerID":"openai","id":"gpt","variant":"high"}}}`)
					return true
				}
				if r.Method == http.MethodGet && r.URL.Path == "/api/session/"+sid {
					writeJSONBody(w, `{"data":{"id":"`+sid+`","agent":"plan","model":{"providerID":"openai","id":"gpt","variant":"high"}}}`)
					return true
				}
				if r.Method == http.MethodPost && r.URL.Path == "/api/session/"+sid+"/prompt" {
					writeJSONBody(w, `{"data":{"id":"msg_first"}}`)
					return true
				}
				return false
			})
			a := nativeQueueAdapter(t, sid, "/repo", f)
			registry := platforms.NewRegistry()
			// Supply browsing methods through the shared fake; creation, rules
			// and delivery still exercise the real native integration and HTTP.
			registry.Register(&platformtest.Fake{
				PlatformID: "opencode",
				CreateSessionFn: func(req platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
					return a.CreateSession(t.Context(), req)
				},
				SendMessageFn:        func(req platforms.SendMessageRequest) error { return a.SendMessage(t.Context(), req) },
				SetPermissionRulesFn: func(req platforms.SetPermissionRulesRequest) error { return a.SetPermissionRules(t.Context(), req) },
				DisposeFn:            func(req platforms.DisposeSessionRequest) error { return a.DisposeSession(t.Context(), req) },
			})
			sessions := sessionsvc.New(registry, sessionsvc.Hooks{})
			var req platforms.CreateSessionRequest
			if err := json.Unmarshal([]byte(`{"Directory":"/repo","creation":{"configured":true,"permissionRules":`+rules+`,"agent":"plan","model":"opencode:openai/gpt","reasoning":"high"}}`), &req); err != nil {
				t.Fatal(err)
			}
			req.Port = f.Port()
			created, err := sessions.CreateConfigured(t.Context(), "opencode", req, req.Creation.PermissionRules)
			if err != nil {
				t.Fatal(err)
			}
			hit, ok := f.find(http.MethodPost, "/api/session")
			if !ok || hit.body["permissions"] == nil || hit.body["agent"] != "plan" || !reflect.DeepEqual(hit.body["model"], map[string]any{"providerID": "openai", "id": "gpt", "variant": "high"}) {
				t.Fatalf("creation lost initial configuration: %#v", hit.body)
			}
			if !created.ConfigurationApplied {
				t.Fatal("creation receipt would trigger a redundant permission PATCH and GET")
			}
			if err := sessions.SendMessage(t.Context(), "opencode", platforms.SendMessageRequest{SessionID: sid, Message: "first", Agent: "plan", Model: "openai/gpt", Reasoning: "high"}); err != nil {
				t.Fatal(err)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			var operations []string
			for _, hit := range f.hits {
				if hit.path != "/api/info" {
					operations = append(operations, hit.method+" "+hit.path)
				}
			}
			want := []string{"POST /api/session", "GET /api/session/" + sid, "POST /api/session/" + sid + "/prompt"}
			if !reflect.DeepEqual(operations, want) {
				t.Fatalf("first delivery operations = %v, want %v", operations, want)
			}
		})
	}
}
