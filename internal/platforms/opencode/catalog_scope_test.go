package opencode

import (
	"context"
	"net/http"
	"testing"

	"github.com/NoUseFreak/ocman/internal/ocv2"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

// OpenCode v2 serves every project from one server, so the catalogs of
// two checkouts on that server must each be read in their own directory
// (and cached separately) to pick up project-local agents, commands
// and model configuration.
func TestDirectoryCatalogScopesV2RequestsByDirectory(t *testing.T) {
	defer ocv2.SetInstalledV2(true)()
	t.Cleanup(func() { catalogCache.invalidatePort("") })
	agents := map[string]string{"/src/alpha": "alpha-agent", "/src/beta": "beta-agent"}
	f := newV2Fake(t, true, func(w http.ResponseWriter, r *http.Request) bool {
		dir := r.URL.Query().Get("location[directory]")
		switch r.URL.Path {
		case "/api/agent":
			writeJSONBody(w, `{"data":[{"id":"`+agents[dir]+`","mode":"primary"}]}`)
		case "/api/command":
			writeJSONBody(w, `{"data":[{"name":"cmd-`+agents[dir]+`"}]}`)
		case "/api/provider", "/api/model":
			writeJSONBody(w, `{"data":[]}`)
		case "/api/model/default":
			writeJSONBody(w, `{"data":null}`)
		default:
			return false
		}
		return true
	})
	a := nativeQueueAdapter(t, "sess-scope", "/src/alpha", f)
	catalogCache.invalidatePort(f.Port())
	for dir, want := range agents {
		got, err := a.DirectoryCatalog(context.Background(), platforms.DirectoryCatalogRequest{Directory: dir, Port: f.Port()})
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Agents) != 1 || got.Agents[0].Name != want || len(got.Commands) != 1 || got.Commands[0].Name != "cmd-"+want {
			t.Fatalf("%s: agents=%+v commands=%+v, want %s", dir, got.Agents, got.Commands, want)
		}
	}
}
