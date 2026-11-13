package claudecode

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
)

func TestCatalogUsesExistingDirectoryExecution(t *testing.T) {
	browse, _ := reloadTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/catalog" {
			t.Errorf("unexpected browsing request %s", r.URL)
		}
		_ = json.NewEncoder(w).Encode(platforms.DirectoryCatalog{Commands: []platforms.SlashCommandEntry{{Name: "probe"}}})
	})
	project, _ := reloadTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sessions":
			if r.URL.Query().Get("runtime") != "1" {
				t.Errorf("not a runtime inventory: %s", r.URL)
			}
			_ = json.NewEncoder(w).Encode([]db.Session{{ID: "s", Directory: "/worktree"}})
		case "/catalog":
			_ = json.NewEncoder(w).Encode(platforms.DirectoryCatalog{Commands: []platforms.SlashCommandEntry{{Name: "live-command"}}})
		default:
			t.Errorf("catalog attempted mutation: %s %s", r.Method, r.URL)
		}
	})
	p := &claudePlatform{Adapter: browse, projects: &claudeProjects{roots: map[string]*claudeProject{
		"/repo": {watched: watched{adapter: project}},
	}}}
	for _, tc := range []struct{ directory, want string }{{"/worktree", "live-command"}, {"/other", "probe"}} {
		catalog, err := p.LiveDirectoryCatalog(t.Context(), platforms.DirectoryCatalogRequest{Directory: tc.directory})
		if err != nil || catalog == nil || len(catalog.Commands) != 1 || catalog.Commands[0].Name != tc.want {
			t.Fatalf("%s catalog = %+v %v", tc.directory, catalog, err)
		}
	}
	dead, _ := reloadTestAdapter(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) })
	p.projects.roots = map[string]*claudeProject{"/repo": {watched: watched{adapter: dead}}}
	if catalog, err := p.LiveDirectoryCatalog(t.Context(), platforms.DirectoryCatalogRequest{Directory: "/worktree"}); err != nil || catalog.Commands[0].Name != "probe" {
		t.Fatalf("dead execution blocked configuration probe: %+v %v", catalog, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := p.LiveDirectoryCatalog(ctx, platforms.DirectoryCatalogRequest{Directory: "/worktree"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation ignored: %v", err)
	}
	p.projects = nil
	if catalog, err := p.LiveDirectoryCatalog(t.Context(), platforms.DirectoryCatalogRequest{Directory: "/worktree"}); err != nil || catalog.Commands[0].Name != "probe" {
		t.Fatalf("browsing-only catalog: %+v %v", catalog, err)
	}
}
