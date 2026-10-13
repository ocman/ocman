package autoapprove

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/module"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
)

func openModuleStore(t *testing.T) *state.DB {
	t.Helper()
	store, err := state.Open(statetest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// testRouter mounts routes on a plain mux, recording which helper each
// pattern used so the guards can be checked.
type testRouter struct {
	mux  *http.ServeMux
	kind map[string]string
}

func (r testRouter) add(kind, pattern string, h http.HandlerFunc) {
	r.kind[pattern] = kind
	r.mux.HandleFunc(pattern, h)
}
func (r testRouter) Handle(p string, h http.HandlerFunc)       { r.add("handle", p, h) }
func (r testRouter) Local(p string, h http.HandlerFunc)        { r.add("local", p, h) }
func (r testRouter) Setting(p string, h http.HandlerFunc)      { r.add("setting", p, h) }
func (r testRouter) LocalSetting(p string, h http.HandlerFunc) { r.add("localSetting", p, h) }

func newTestModule(store *state.DB) *Module {
	pass := func(h http.HandlerFunc) http.HandlerFunc { return h }
	deps := ModuleDeps{Store: store, Guards: module.Guards{Auth: pass, Localhost: pass}}
	if store != nil { // a typed nil would defeat the pipeline's nil checks
		deps.Engine.Store = store
	}
	return NewModule(deps)
}

func serve(m *Module, method, path, body string) *httptest.ResponseRecorder {
	r := testRouter{mux: http.NewServeMux(), kind: map[string]string{}}
	m.Routes(r)
	rec := httptest.NewRecorder()
	r.mux.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	return rec
}

// Every route is behind the auth guard, and settings routes broadcast.
func TestModuleRoutesAreAuthGuarded(t *testing.T) {
	denied := func(http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) }
	}
	m := NewModule(ModuleDeps{Store: openModuleStore(t), Guards: module.Guards{Auth: denied}})
	r := testRouter{mux: http.NewServeMux(), kind: map[string]string{}}
	m.Routes(r)
	for pattern, kind := range r.kind {
		method, path, ok := strings.Cut(pattern, " ")
		if !ok {
			method, path = http.MethodGet, pattern
		}
		path = strings.ReplaceAll(path, "{id}", "ses-1")
		rec := httptest.NewRecorder()
		r.mux.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s answered %d without auth", pattern, rec.Code)
		}
		if strings.HasPrefix(path, "/api/settings/") && path != "/api/settings/judge-model/options" && kind != "setting" {
			t.Errorf("%s mounted as %s, want setting", pattern, kind)
		}
	}
	if len(r.kind) != 8 {
		t.Fatalf("routes = %v", r.kind)
	}
}

func TestModuleStartSeedsJudgeDelay(t *testing.T) {
	store := openModuleStore(t)
	if err := store.SetJudgeDelayMs(t.Context(), 1234); err != nil {
		t.Fatal(err)
	}
	m := newTestModule(store)
	if err := m.Start(t.Context()); err != nil || m.Service().JudgeDelayMs() != 1234 {
		t.Fatalf("seeded delay = %d, %v", m.Service().JudgeDelayMs(), err)
	}
	bare := newTestModule(nil)
	if err := bare.Start(t.Context()); err != nil || bare.Service().JudgeDelayMs() != state.DefaultJudgeDelayMs {
		t.Fatalf("default delay = %d, %v", bare.Service().JudgeDelayMs(), err)
	}
}

func TestModuleJudgeDelaySetting(t *testing.T) {
	m := newTestModule(openModuleStore(t))
	if rec := serve(m, http.MethodGet, "/api/settings/judge-delay", ""); !strings.Contains(rec.Body.String(), `"delayMs":`) {
		t.Fatalf("GET: %d %s", rec.Code, rec.Body)
	}
	rec := serve(m, http.MethodPost, "/api/settings/judge-delay", `{"delayMs":250}`)
	if rec.Code != 200 || m.Service().JudgeDelayMs() != 250 {
		t.Fatalf("POST: %d %s, cached %d", rec.Code, rec.Body, m.Service().JudgeDelayMs())
	}
	if rec := serve(m, http.MethodGet, "/api/settings/judge-delay", ""); !strings.Contains(rec.Body.String(), `"delayMs":250`) {
		t.Fatalf("GET after POST: %s", rec.Body)
	}
	if rec := serve(m, http.MethodDelete, "/api/settings/judge-delay", ""); rec.Code != 405 {
		t.Fatalf("DELETE: %d", rec.Code)
	}
	if rec := serve(newTestModule(nil), http.MethodPost, "/api/settings/judge-delay", `{"delayMs":1}`); rec.Code != 503 {
		t.Fatalf("no store: %d", rec.Code)
	}
}

func TestModulePromptSections(t *testing.T) {
	m := newTestModule(openModuleStore(t))
	if rec := serve(m, http.MethodGet, "/api/settings/prompt-sections", ""); strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("empty GET: %s", rec.Body)
	}
	rec := serve(m, http.MethodPost, "/api/settings/prompt-sections", `[{"title":"Rules","content":"no rm"}]`)
	if rec.Code != 200 {
		t.Fatalf("POST: %d %s", rec.Code, rec.Body)
	}
	if rec := serve(m, http.MethodGet, "/api/settings/prompt-sections", ""); !strings.Contains(rec.Body.String(), "no rm") {
		t.Fatalf("GET after POST: %s", rec.Body)
	}
	if rec := serve(newTestModule(nil), http.MethodGet, "/api/settings/prompt-sections", ""); strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("no store GET: %s", rec.Body)
	}
	if rec := serve(newTestModule(nil), http.MethodPost, "/api/settings/prompt-sections", `[]`); rec.Code != 503 {
		t.Fatalf("no store POST: %d", rec.Code)
	}
	if rec := serve(m, http.MethodPut, "/api/settings/prompt-sections", ""); rec.Code != 405 {
		t.Fatalf("PUT: %d", rec.Code)
	}
}

func decodeModel(t *testing.T, body []byte) string {
	t.Helper()
	var resp struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decoding response %q: %v", body, err)
	}
	return resp.Model
}

func TestModuleJudgeModel(t *testing.T) {
	store := openModuleStore(t)
	m := newTestModule(store)
	if got := decodeModel(t, serve(m, http.MethodGet, "/api/settings/judge-model", "").Body.Bytes()); got != "" {
		t.Fatalf("unset model = %q", got)
	}
	rec := serve(m, http.MethodPost, "/api/settings/judge-model", `{"model":"openrouter/anthropic/claude"}`)
	if rec.Code != 200 || decodeModel(t, rec.Body.Bytes()) != "openrouter/anthropic/claude" {
		t.Fatalf("POST: %d %s", rec.Code, rec.Body)
	}
	if provider, modelID := m.Service().JudgeModel(); provider != "openrouter" || modelID != "anthropic/claude" {
		t.Fatalf("judge not updated: %q %q", provider, modelID)
	}
	if got := decodeModel(t, serve(m, http.MethodGet, "/api/settings/judge-model", "").Body.Bytes()); got != "openrouter/anthropic/claude" {
		t.Fatalf("stored model = %q", got)
	}
	// Empty clears the setting and reverts the judge to the default.
	if rec := serve(m, http.MethodPost, "/api/settings/judge-model", `{"model":""}`); rec.Code != 200 {
		t.Fatalf("clear: %d", rec.Code)
	}
	if provider, modelID := m.Service().JudgeModel(); provider != "anthropic" || modelID != "claude-haiku-4-5" {
		t.Fatalf("judge not reverted: %q %q", provider, modelID)
	}
	if rec := serve(m, http.MethodPost, "/api/settings/judge-model", `{bad`); rec.Code != 400 {
		t.Fatalf("invalid JSON: %d", rec.Code)
	}
	if rec := serve(newTestModule(nil), http.MethodPost, "/api/settings/judge-model", `{"model":"a/b"}`); rec.Code != 503 {
		t.Fatalf("no store: %d", rec.Code)
	}
	if rec := serve(m, http.MethodDelete, "/api/settings/judge-model", ""); rec.Code != 405 {
		t.Fatalf("DELETE: %d", rec.Code)
	}
}

func TestModuleJudgeModelOptions(t *testing.T) {
	decode := func(body []byte) (models []string, def string) {
		t.Helper()
		var resp struct {
			Models  []string `json:"models"`
			Default string   `json:"default"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		return resp.Models, resp.Default
	}
	options := []string{"anthropic/x", "openai/y"}
	m := newTestModule(nil)
	m.ModelOptions = func(context.Context) []string { return options }
	rec := serve(m, http.MethodGet, "/api/settings/judge-model/options", "")
	models, def := decode(rec.Body.Bytes())
	if rec.Code != 200 || len(models) != 2 || models[0] != "anthropic/x" || def != DefaultJudgeModel {
		t.Fatalf("options: %d %s", rec.Code, rec.Body)
	}
	// No reachable instance: an empty array, never a JSON null.
	options = nil
	if models, _ = decode(serve(m, http.MethodGet, "/api/settings/judge-model/options", "").Body.Bytes()); models == nil || len(models) != 0 {
		t.Fatalf("empty models = %v", models)
	}
	if rec := serve(m, http.MethodPost, "/api/settings/judge-model/options", ""); rec.Code != 405 {
		t.Fatalf("POST: %d", rec.Code)
	}
}

func TestJudgeEndpointSettings(t *testing.T) {
	store := openModuleStore(t)
	m := newTestModule(store)
	call := func(method, body string) *httptest.ResponseRecorder {
		return serve(m, method, "/api/settings/judge-endpoint", body)
	}
	get := call(http.MethodGet, "")
	if get.Code != 200 || !strings.Contains(get.Body.String(), `"minSafeProbability":0.99`) {
		t.Fatalf("default: %d %s", get.Code, get.Body)
	}
	post := call(http.MethodPost, `{"format":"typesafe","endpoint":"http://localhost:8080/v1/systemone","model":"local","apiKey":"secret-test-key","minSafeProbability":0.99}`)
	if post.Code != 200 || strings.Contains(post.Body.String(), "secret-test-key") || !strings.Contains(post.Body.String(), `"apiKeySet":true`) {
		t.Fatalf("save: %d %s", post.Code, post.Body)
	}
	get = call(http.MethodGet, "")
	if strings.Contains(get.Body.String(), "secret-test-key") || get.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("key leaked: %s", get.Body)
	}
	post = call(http.MethodPost, `{"format":"typesafe","endpoint":"http://localhost:8080/v1/systemone","minSafeProbability":0.98}`)
	config, err := LoadJudgeEndpoint(t.Context(), store)
	if post.Code != 200 || err != nil || config.APIKey != "secret-test-key" {
		t.Fatalf("key not preserved: %d %+v %v", post.Code, config, err)
	}
	post = call(http.MethodPost, `{"format":"","endpoint":"","apiKey":"","minSafeProbability":0.99}`)
	config, err = LoadJudgeEndpoint(t.Context(), store)
	if post.Code != 200 || err != nil || config.APIKey != "" || config.Format != "" {
		t.Fatalf("clear: %d %+v %v", post.Code, config, err)
	}
	for _, body := range []string{`{`, `{"format":"unknown"}`, `{"format":"openai","endpoint":"file:///tmp/model","model":"local","minSafeProbability":0.99}`} {
		if rec := call(http.MethodPost, body); rec.Code != 400 {
			t.Errorf("invalid body accepted: %d %s", rec.Code, rec.Body)
		}
	}
	if rec := call(http.MethodDelete, ""); rec.Code != 405 {
		t.Fatalf("method: %d", rec.Code)
	}
	if rec := serve(newTestModule(nil), http.MethodGet, "/api/settings/judge-endpoint", ""); rec.Code != 503 {
		t.Fatalf("no store: %d", rec.Code)
	}
}

func TestJudgeEndpointURLChangeDoesNotReuseCredentials(t *testing.T) {
	for _, tc := range []struct{ name, keyField, wantKey string }{
		{"omitted key", "", ""},
		{"replacement key", `,"apiKey":"replacement-key"`, "replacement-key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := openModuleStore(t)
			m := newTestModule(store)
			call := func(method, body string) *httptest.ResponseRecorder {
				return serve(m, method, "/api/settings/judge-endpoint", body)
			}
			if seed := call(http.MethodPost, `{"format":"typesafe","endpoint":"https://old.example/v1/systemone","apiKey":"original-key","minSafeProbability":0.99}`); seed.Code != http.StatusOK {
				t.Fatalf("seed: %d %s", seed.Code, seed.Body)
			}
			post := call(http.MethodPost, `{"format":"typesafe","endpoint":"https://new.example/v1/systemone","minSafeProbability":0.99`+tc.keyField+`}`)
			config, err := LoadJudgeEndpoint(t.Context(), store)
			if err != nil || config.APIKey != tc.wantKey {
				t.Fatalf("stored key = %q, error = %v", config.APIKey, err)
			}
			wantSet := `"apiKeySet":false`
			if tc.wantKey != "" {
				wantSet = `"apiKeySet":true`
			}
			for _, rec := range []*httptest.ResponseRecorder{post, call(http.MethodGet, "")} {
				body := rec.Body.String()
				if rec.Code != http.StatusOK || strings.Contains(body, "original-key") || strings.Contains(body, "replacement-key") || !strings.Contains(body, wantSet) {
					t.Fatalf("response: %d %s", rec.Code, body)
				}
			}
		})
	}
}
