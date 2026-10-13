package previews

import (
	"net/http"
	"testing"

	"github.com/NoUseFreak/ocman/internal/linkpreview"
	"github.com/NoUseFreak/ocman/internal/module"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
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
	db, err := state.Open(statetest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
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
		Guards:          moduleGuards(),
	})
	return h
}

func (h *harness) withResolvers(r ...linkpreview.Resolver) *harness {
	h.Resolvers = r
	return h
}

func (h *harness) mux() *http.ServeMux {
	mux := http.NewServeMux()
	h.Routes(testRouter{mux})
	return mux
}

// registerForgejoClient configures a tea/env Forgejo login for host.
func registerForgejoClient(h *harness, host, apiBase string) {
	h.forges.Forgejo = []ForgejoLogin{{Host: host, BaseURL: apiBase, Token: "tok"}}
}

type testRouter struct{ mux *http.ServeMux }

func (r testRouter) Handle(p string, h http.HandlerFunc)       { r.mux.HandleFunc(p, h) }
func (r testRouter) Local(p string, h http.HandlerFunc)        { r.mux.HandleFunc(p, h) }
func (r testRouter) Setting(p string, h http.HandlerFunc)      { r.mux.HandleFunc(p, h) }
func (r testRouter) LocalSetting(p string, h http.HandlerFunc) { r.mux.HandleFunc(p, h) }

func moduleGuards() module.Guards {
	pass := func(h http.HandlerFunc) http.HandlerFunc { return h }
	return module.Guards{Auth: pass, Localhost: pass}
}
