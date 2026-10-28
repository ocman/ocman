package previews

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/linkpreview"
	"github.com/ocman/ocman/internal/module/moduletest"
	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/state/statetest"
)

// harness is the module as the server wires it, with the server's guards
// replaced: a request has app access unless it came through a proxy
// (X-Forwarded-For), and remotes is the set of connected remotes.
type harness struct {
	*Module
	remotes map[string]bool
	forges  Forges
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	db := statetest.Open(t)
	return harnessOn(db)
}

func harnessOn(db *state.DB) *harness {
	h := &harness{remotes: map[string]bool{}}
	h.Module = NewModule(Deps{
		Store:           db,
		Access:          func(r *http.Request) bool { return r.Header.Get("X-Forwarded-For") == "" },
		RemoteConnected: func(id string) bool { return h.remotes[id] },
		PublicURL:       func(p string) string { return "http://127.0.0.1:0" + p },
		Forges:          func() Forges { return h.forges },
	})
	return h
}

func (h *harness) withResolvers(r ...linkpreview.Resolver) *harness {
	h.Resolvers = r
	return h
}

func (h *harness) mux() *http.ServeMux {
	return moduletest.Mount(h)
}

// registerForgejoClient configures a tea/env Forgejo login for host.
func registerForgejoClient(h *harness, host, apiBase string) {
	h.forges.Forgejo = []ForgejoLogin{{Host: host, BaseURL: apiBase, Token: "tok"}}
}

// Without a state DB every credential and rule route answers 503 instead of
// panicking.
func TestRoutesWithoutStore(t *testing.T) {
	mux := harnessOn(nil).mux()
	for _, c := range []struct{ method, target string }{
		{http.MethodGet, "/api/previews/providers"}, {http.MethodGet, "/api/previews/apps"},
		{http.MethodGet, previewCallback}, {http.MethodGet, "/api/settings/link-preview-rules"},
		{http.MethodPost, "/api/previews/resolve"}, {http.MethodPost, "/api/previews/token"},
		{http.MethodPost, "/api/previews/connect"}, {http.MethodPost, "/api/previews/disconnect"},
		{http.MethodPost, "/api/previews/apps/save"}, {http.MethodPost, "/api/previews/apps/remove"},
	} {
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, httptest.NewRequest(c.method, c.target, strings.NewReader("{}")))
		if rr.Code != http.StatusServiceUnavailable {
			t.Errorf("%s %s = %d", c.method, c.target, rr.Code)
		}
	}
}

func TestForgesWithoutSource(t *testing.T) {
	if got := (&Module{}).forges(); len(got.Forgejo) != 0 {
		t.Fatal(got)
	}
}
