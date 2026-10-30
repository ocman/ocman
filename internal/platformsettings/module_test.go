package platformsettings

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/module"
	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/state/statetest"
)

type router struct {
	module.Router
	path    string
	handler http.HandlerFunc
}

func (r *router) LocalSetting(path string, handler http.HandlerFunc) {
	if path == "/api/settings/platforms" {
		r.path, r.handler = path, handler
	}
}

func TestSettings(t *testing.T) {
	path := statetest.Path(t)
	store, err := state.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	m := New(store)
	r := &router{}
	m.Routes(r)
	if m.Name() != "platform-settings" || r.path != "/api/settings/platforms" {
		t.Fatalf("route: %+v", r)
	}
	request := func(method, body string, status int, contains string) {
		t.Helper()
		w := httptest.NewRecorder()
		r.handler(w, httptest.NewRequest(method, r.path, strings.NewReader(body)))
		if w.Code != status || !strings.Contains(w.Body.String(), contains) {
			t.Fatalf("%s %s: %d %s", method, body, w.Code, w.Body.String())
		}
	}
	request("GET", "", 200, `"enabled":["opencode","claudecode"]`)
	for _, body := range []string{`{}`, `{"enabled":[]}`, `{"enabled":["unknown"]}`, `{"enabled":["opencode","opencode"]}`, `invalid`} {
		request("POST", body, 400, "")
	}
	request("PUT", "", 405, "")
	request("POST", `{"enabled":["opencode","codex"]}`, 200, `"enabled":["codex","opencode"]`)
	request("POST", `{"enabled":["claudecode"]}`, 200, `"enabled":["claudecode"]`)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = state.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	m.store = store
	request("GET", "", 200, `"enabled":["claudecode"]`)
	selected, err := Resolve(t.Context(), store, Default, false)
	if err != nil || selected != "claudecode" {
		t.Fatalf("saved startup: %q %v", selected, err)
	}
	selected, err = Resolve(t.Context(), store, "opencode", true)
	if err != nil || selected != "opencode" {
		t.Fatalf("CLI override: %q %v", selected, err)
	}
	if err := store.SetSetting(t.Context(), setting, "unknown"); err != nil {
		t.Fatal(err)
	}
	request("GET", "", 500, "")
	if _, err := Resolve(t.Context(), store, Default, false); err == nil {
		t.Fatal("invalid saved selection accepted")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	request("GET", "", 500, "")
	request("POST", `{"enabled":["opencode"]}`, 500, "")
	m.store = nil
	request("GET", "", 503, "unavailable")
}
