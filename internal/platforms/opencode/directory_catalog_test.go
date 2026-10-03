package opencode

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

// The new-conversation composer reads the directory's instance catalogs
// without a session; a worktree path folds to the project's instance.
func TestDirectoryCatalogReadsInstanceForDirectory(t *testing.T) {
	const dir = "/src/repo"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/agent":
			_, _ = io.WriteString(w, `[{"name":"build","mode":"primary"},{"name":"reviewer","mode":"subagent"}]`)
		case "/command":
			_, _ = io.WriteString(w, `[{"name":"review","description":"Review","source":"command"}]`)
		case "/provider":
			_, _ = io.WriteString(w, `{"all":[],"connected":[],"default":{}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	withTestPort(t, dir, strings.TrimPrefix(server.URL, "http://127.0.0.1:"))
	a := New(newTestDBWithSession(t, "ses-1", dir), nil)

	catalog, err := a.DirectoryCatalog(context.Background(), platforms.DirectoryCatalogRequest{Directory: "/src/.worktrees/repo/feat"})
	if err != nil {
		t.Fatalf("DirectoryCatalog: %v", err)
	}
	if !catalog.LiveConnection {
		t.Fatal("expected a live connection for the folded project directory")
	}
	if len(catalog.Agents) != 2 || catalog.Agents[0].Name != "build" {
		t.Fatalf("agents = %+v", catalog.Agents)
	}
	if len(catalog.Commands) != 1 || catalog.Commands[0].Name != "review" {
		t.Fatalf("commands = %+v", catalog.Commands)
	}
	if catalog.Models == nil {
		t.Fatal("models missing")
	}
}

func TestDirectoryCatalogWithoutInstanceFallsBackToHistory(t *testing.T) {
	withTestPort(t, "/elsewhere", "1")
	a := New(newTestDBWithSession(t, "ses-1", "/src/repo"), nil)
	catalog, err := a.DirectoryCatalog(context.Background(), platforms.DirectoryCatalogRequest{Directory: "/src/repo"})
	if err != nil {
		t.Fatalf("DirectoryCatalog: %v", err)
	}
	if catalog.LiveConnection || len(catalog.Agents) != 0 || len(catalog.Commands) != 0 || catalog.Models == nil {
		t.Fatalf("catalog = %+v", catalog)
	}
}

// A port the caller just ensured wins over (possibly stale) discovery.
func TestDirectoryCatalogUsesPinnedPort(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/agent" {
			_, _ = io.WriteString(w, `[{"name":"build"}]`)
			return
		}
		_, _ = io.WriteString(w, `[]`)
	}))
	defer server.Close()
	withTestPort(t, "/elsewhere", "1")
	a := New(newTestDBWithSession(t, "ses-1", "/src/repo"), nil)
	catalog, err := a.DirectoryCatalog(context.Background(), platforms.DirectoryCatalogRequest{Directory: "/src/repo", Port: strings.TrimPrefix(server.URL, "http://127.0.0.1:")})
	if err != nil {
		t.Fatalf("DirectoryCatalog: %v", err)
	}
	if !catalog.LiveConnection || len(catalog.Agents) != 1 {
		t.Fatalf("catalog = %+v", catalog)
	}
}
