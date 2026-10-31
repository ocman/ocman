package pluginsmod

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/state/statetest"
	plugins "github.com/ocman/ocman/sdk/protocol"
)

func TestSessionProjectAssociationPersistsAndCannotBeReassigned(t *testing.T) {
	path := statetest.Path(t)
	database, err := state.Open(path, Schema)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	store := NewStore(database)
	ctx := t.Context()
	for range 2 {
		if err := store.associateSessionProject(ctx, "reader", "/private/source", "/projects/platform-service"); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct{ plugin, dir, target string }{
		{"reader", "/private/source", "/projects/other"},
		{"other", "/private/source", "/projects/platform-service"},
		{"reader", "relative", "/projects/platform-service"},
		{"reader", "/private/source", "relative"},
		{"reader", "/same", "/same"},
	} {
		if err := store.associateSessionProject(ctx, tc.plugin, tc.dir, tc.target); err == nil {
			t.Fatalf("unsafe association accepted: %+v", tc)
		}
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := state.Open(path, Schema)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reopened.Close() })
	restarted := NewStore(reopened)
	got, err := restarted.sessionProjectAssociations(ctx)
	if err != nil || len(got) != 1 || got["/private/source"] != "/projects/platform-service" {
		t.Fatalf("mapping lost after restart: %+v %v", got, err)
	}
	if _, err := store.sessionProjectAssociations(ctx); err == nil {
		t.Fatal("association read failure hidden")
	}
	if err := store.associateSessionProject(ctx, "reader", "/private/source", "/projects/platform-service"); err == nil {
		t.Fatal("association write failure hidden")
	}
}

func TestAssociateProjectsPreservesSourceStatsAndIncludesMissingTarget(t *testing.T) {
	store := openPluginStore(t)
	if err := store.associateSessionProject(t.Context(), "reader", "/private/source", "/projects/platform-service"); err != nil {
		t.Fatal(err)
	}
	m := &Module{plugins: store}
	for _, hasTarget := range []bool{false, true} {
		projects := []db.ProjectStats{{Directory: "/private/source", SessionCount: 2, TotalCost: 3}}
		if hasTarget {
			projects = append(projects, db.ProjectStats{Directory: "/projects/platform-service", SessionCount: 4})
		}
		got, err := m.AssociateProjects(t.Context(), projects)
		if err != nil || len(got) != 2 || got[0].Directory != "/private/source" || got[0].AssociatedDirectory != "/projects/platform-service" || got[0].SessionCount != 2 || got[0].TotalCost != 3 {
			t.Fatalf("association changed checkout or totals: %+v %v", got, err)
		}
		if got[1].Directory != "/projects/platform-service" || hasTarget && got[1].SessionCount != 4 || !hasTarget && got[1].SessionCount != 0 {
			t.Fatalf("association duplicated or lost target stats: %+v", got)
		}
	}
	if got, err := new(Module).AssociateProjects(t.Context(), nil); err != nil || got != nil {
		t.Fatalf("unavailable plugins changed inventory: %+v %v", got, err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := m.AssociateProjects(canceled, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("association query error hidden: %v", err)
	}
}

func TestSourceSessionAssociationKeepsReadRestrictionsAndDirectory(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source-2059222746")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "app.go"), []byte("package app"), 0600); err != nil {
		t.Fatal(err)
	}
	creator := new(sourceTestSessions)
	create, lookup := localPlatforms("opencode")
	database := statetest.Open(t, Schema)
	m := &Module{store: database, plugins: NewStore(database), deps: Deps{
		Local: func() hostsvc.Host { return modelTestHost{} }, Sessions: creator, CreatePlatform: create, Platform: lookup,
		Enqueue: func(context.Context, string, bool, platforms.SendMessageRequest) error { return nil },
	}}
	params := plugins.SessionRunParams{WorkspaceID: "workspace-a", AssociatedProjectID: "platform-service", Subpath: filepath.Base(source), ReadOnly: true, Prompt: "Review source"}
	config := plugins.Config{Project: root, AssociatedProject: "/projects/platform-service"}
	if _, err := m.startSessionRun(t.Context(), "reader", config, params, source, "source-1"); err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(source)
	if err != nil {
		t.Fatal(err)
	}
	if creator.directory != canonical || len(creator.rules) != 3 || creator.rules[0].Action != "deny" || creator.rules[1].Permission != "read" {
		t.Fatalf("association changed source execution or permissions: %s %+v", creator.directory, creator.rules)
	}
	// Retained history still groups after the source copy is removed.
	if err := os.Remove(filepath.Join(source, "app.go")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	projects, err := m.AssociateProjects(t.Context(), []db.ProjectStats{{Directory: canonical}})
	if err != nil || len(projects) != 2 || projects[0].AssociatedDirectory != config.AssociatedProject {
		t.Fatalf("deleted source lost association: %+v %v", projects, err)
	}
}
