package claudecode

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/provider"
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
	p := &claudePlatform{Adapter: browse, projects: &claudeProjects{sessions: map[string]*provider.Adapter{"s": project}, roots: map[string]*claudeProject{
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

func TestCatalogSharedInventoryDoesNotEstablishExecutionOwnership(t *testing.T) {
	for _, tc := range []struct {
		name, projectRoot string
		attached          bool
	}{
		{name: "unattached recorded owner"},
		{name: "two attached supervisors", attached: true, projectRoot: "recorded"},
		{name: "legacy ownership without project root", attached: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			statePath := filepath.Join(t.TempDir(), "claude-sessions.json")
			owned := map[string]any{"directory": root, "started": true}
			if tc.projectRoot != "" {
				owned["projectRoot"] = root
			}
			raw, err := json.Marshal(map[string]any{"owned-session": owned})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(statePath, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			catalog := func(name string) platforms.DirectoryCatalog {
				return platforms.DirectoryCatalog{Commands: []platforms.SlashCommandEntry{{Name: name}}}
			}
			browse, _ := reloadTestAdapter(t, func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(catalog("probe"))
			})
			peer := func(name string) *provider.Adapter {
				a, _ := reloadTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
					switch r.URL.Path {
					case "/sessions":
						// Both modern and older same-configuration supervisors can
						// list sessions from the shared ownership/transcript store.
						_ = json.NewEncoder(w).Encode([]db.Session{{ID: "owned-session", Directory: root}})
					case "/catalog":
						if name == "unrelated" {
							t.Error("shared inventory granted catalog execution ownership")
						}
						_ = json.NewEncoder(w).Encode(catalog(name))
					default:
						t.Errorf("unexpected request %s %s", r.Method, r.URL)
					}
				})
				return a
			}
			p := &claudePlatform{Adapter: browse, projects: &claudeProjects{statePath: statePath, roots: map[string]*claudeProject{
				"/unrelated-project": {watched: watched{adapter: peer("unrelated")}},
			}}}
			want := "probe"
			if tc.attached {
				p.projects.roots[root] = &claudeProject{watched: watched{adapter: peer("announced")}}
				want = "announced"
			}
			for range 20 {
				got, err := p.LiveDirectoryCatalog(t.Context(), platforms.DirectoryCatalogRequest{Directory: root})
				if err != nil || got == nil || len(got.Commands) != 1 || got.Commands[0].Name != want {
					t.Fatalf("catalog = %+v %v, want %s", got, err, want)
				}
			}
		})
	}
}
