package opencode

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/opencode/api"
)

// The new-conversation composer reads the directory's instance catalogs
// without a session; a worktree path folds to the project's instance.
func TestStoredDirectoryCatalogReadsInstanceForDirectory(t *testing.T) {
	const dir = "/src/repo"
	port := fakePort(t, func(w http.ResponseWriter, r *http.Request) {
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
	})
	withTestPort(t, dir, port)
	a := New(newTestDBWithSession(t, "ses-1", dir), nil, nil, ocapi.Auth{})

	catalog, err := a.DirectoryCatalogFromStored(context.Background(), platforms.DirectoryCatalogRequest{Directory: "/src/.worktrees/repo/feat"}, nil, db.SessionDefaults{}, true)
	if err != nil {
		t.Fatalf("DirectoryCatalogFromStored: %v", err)
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

// A port the caller just ensured wins over (possibly stale) discovery.
func TestStoredDirectoryCatalogUsesPinnedPort(t *testing.T) {
	port := fakePort(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/agent" {
			_, _ = io.WriteString(w, `[{"name":"build"}]`)
			return
		}
		_, _ = io.WriteString(w, `[]`)
	})
	withTestPort(t, "/elsewhere", "1")
	a := New(newTestDBWithSession(t, "ses-1", "/src/repo"), nil, nil, ocapi.Auth{})
	catalog, err := a.DirectoryCatalogFromStored(context.Background(), platforms.DirectoryCatalogRequest{Directory: "/src/repo", Port: port}, nil, db.SessionDefaults{}, true)
	if err != nil {
		t.Fatalf("DirectoryCatalogFromStored: %v", err)
	}
	if !catalog.LiveConnection || len(catalog.Agents) != 1 {
		t.Fatalf("catalog = %+v", catalog)
	}
}
