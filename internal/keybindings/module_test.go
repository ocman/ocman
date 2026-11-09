package keybindings

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ocman/ocman/internal/module/moduletest"
)

func TestCatalog(t *testing.T) {
	m := &Module{}
	if m.Name() != "keybindings" {
		t.Fatalf("name = %q", m.Name())
	}
	w := httptest.NewRecorder()
	moduletest.Mount(m).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/keybindings", nil))
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("response = %d %v: %s", w.Code, w.Header(), w.Body.String())
	}
	var catalog map[string][]struct {
		Code  string `json:"code"`
		Alt   bool   `json:"alt"`
		Shift bool   `json:"shift"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &catalog); err != nil {
		t.Fatal(err)
	}
	if len(catalog) != 21 {
		t.Fatalf("catalog has %d actions, want 21", len(catalog))
	}
	for id, bindings := range catalog {
		if len(bindings) == 0 {
			t.Errorf("%s has no default bindings", id)
		}
		for _, binding := range bindings {
			if binding.Code == "" {
				t.Errorf("%s has an empty physical key code", id)
			}
		}
	}
	if got := catalog["site.command-palette"]; len(got) != 1 || got[0].Code != "Space" || !got[0].Alt || got[0].Shift {
		t.Fatalf("command palette = %+v", got)
	}
	if got := catalog["session.new-session"]; len(got) != 2 || got[0].Code != "KeyC" || got[1].Code != "KeyT" {
		t.Fatalf("new-session alternatives = %+v", got)
	}
	if len(catalog["site.toggle-shortcuts"]) != 4 || len(catalog["inbox.archive"]) != 2 {
		t.Fatal("missing layout or archive alternatives")
	}
}

func TestRouteGuards(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		w := httptest.NewRecorder()
		moduletest.Mount(&Module{}).ServeHTTP(w, httptest.NewRequest(method, "/api/keybindings", nil))
		if w.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s = %d, want 405", method, w.Code)
		}
	}
	mux := http.NewServeMux()
	(&Module{}).Routes(moduletest.Router{Mux: mux, Auth: func(http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) }
	}})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/keybindings", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated = %d, want 401", w.Code)
	}
}
