package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
)

func TestProjectRootForDirectory(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"/src/foo", "/src/foo"},
		{"/src/foo/", "/src/foo"},
		{"/src/.worktrees/foo/feature-a", "/src/foo"},
		{"/src/.worktrees/foo/feature-a/sub", "/src/foo"},
		// Too few components after .worktrees -> unchanged.
		{"/src/.worktrees/foo", "/src/.worktrees/foo"},
		// Worktree directly under "/" can't be folded safely.
		{"/.worktrees/foo/bar", "/.worktrees/foo/bar"},
		// Relative path starting with .worktrees has no prefix.
		{".worktrees/foo/bar", ".worktrees/foo/bar"},
	}
	for _, c := range cases {
		if got := projectRootForDirectory(c.in); got != c.want {
			t.Errorf("projectRootForDirectory(%q) = %q; want %q", c.in, got, c.want)
		}
	}
}

func TestFoldWorktreeProjects(t *testing.T) {
	in := []db.ProjectStats{
		{Directory: "/src/foo", SessionCount: 2, MessageCount: 5, LastUsed: 100, TotalTokensIn: 10},
		{Directory: "/src/.worktrees/foo/feature-a", SessionCount: 1, MessageCount: 3, LastUsed: 200, TotalTokensIn: 4},
		{Directory: "/src/.worktrees/foo/feature-b", SessionCount: 3, MessageCount: 1, LastUsed: 50, TotalTokensIn: 6},
		{Directory: "/src/bar", SessionCount: 1, LastUsed: 70},
		// Same repo path on a remote must stay separate.
		{Directory: "/src/.worktrees/foo/wt", RemoteID: "r1", RemoteName: "box", SessionCount: 9, LastUsed: 999},
	}
	out := foldWorktreeProjects(in)

	byKey := map[string]db.ProjectStats{}
	for _, p := range out {
		byKey[p.RemoteID+"|"+p.Directory] = p
	}
	if len(out) != 3 {
		t.Fatalf("expected 3 folded projects, got %d: %+v", len(out), out)
	}

	foo := byKey["|/src/foo"]
	if foo.SessionCount != 6 || foo.MessageCount != 9 || foo.TotalTokensIn != 20 {
		t.Errorf("foo aggregate wrong: %+v", foo)
	}
	if foo.LastUsed != 200 {
		t.Errorf("foo LastUsed = %d, want 200 (max)", foo.LastUsed)
	}
	if _, ok := byKey["|/src/bar"]; !ok {
		t.Errorf("bar missing from output: %+v", out)
	}

	rem := byKey["r1|/src/foo"]
	if rem.RemoteID != "r1" || rem.SessionCount != 9 {
		t.Errorf("remote foo not kept separate: %+v", rem)
	}
}

func postProjectArchive(t *testing.T, srv *Server, dir string, archived bool) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"directory": dir, "archived": archived})
	req := httptest.NewRequest(http.MethodPost, "/api/project/archive", strings.NewReader(string(body)))
	rr := httptest.NewRecorder()
	srv.handleProjectArchive(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("archive %q=%v: expected 200, got %d: %s", dir, archived, rr.Code, rr.Body.String())
	}
}

func getProjects(t *testing.T, srv *Server) []db.ProjectStats {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/projects", nil)
	rr := httptest.NewRecorder()
	srv.handleProjects(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("handleProjects: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var projects []db.ProjectStats
	if err := json.Unmarshal(rr.Body.Bytes(), &projects); err != nil {
		t.Fatalf("invalid projects JSON: %v", err)
	}
	return projects
}

// TestProjectArchive_OverlayAndAutoUnarchive covers the full loop:
// archiving a project marks it in /api/projects; newer session activity
// auto-unarchives it.
func TestProjectArchive_OverlayAndAutoUnarchive(t *testing.T) {
	srv, rawDB := testServerWithRawDB(t)
	defer rawDB.Close()

	if _, err := rawDB.Exec(
		`INSERT INTO session (id, title, directory, time_created, time_updated)
		 VALUES ('s-1', 'one', '/repo/one', 1000, 1000)`,
	); err != nil {
		t.Fatalf("seeding session: %v", err)
	}
	if err := srv.refreshProjectsIndex(); err != nil {
		t.Fatalf("projects refresh: %v", err)
	}

	// Archive the project; the overlay should mark it.
	postProjectArchive(t, srv, "/repo/one", true)
	projects := getProjects(t, srv)
	if len(projects) != 1 || !projects[0].Archived {
		t.Fatalf("expected project archived, got %+v", projects)
	}

	// New activity newer than archived_at auto-unarchives. ArchiveProject
	// stamps archived_at = now (ms); use a far-future time_updated so the
	// comparison is unambiguous regardless of clock.
	if _, err := rawDB.Exec(
		`UPDATE session SET time_updated = 9999999999999 WHERE id = 's-1'`,
	); err != nil {
		t.Fatalf("bumping activity: %v", err)
	}
	if err := srv.refreshProjectsIndex(); err != nil {
		t.Fatalf("projects refresh 2: %v", err)
	}
	projects = getProjects(t, srv)
	if len(projects) != 1 || projects[0].Archived {
		t.Fatalf("expected project auto-unarchived after newer activity, got %+v", projects)
	}
	// The marker should be gone from state.db too.
	archived, _ := srv.stateDB.ArchivedProjects()
	if len(archived) != 0 {
		t.Errorf("expected archive marker deleted, got %v", archived)
	}
}

// TestProjectArchive_FoldsWorktreeToRoot verifies the archive key is the
// folded project root, so archiving via a worktree dir hides the repo.
func TestProjectArchive_FoldsWorktreeToRoot(t *testing.T) {
	srv, rawDB := testServerWithRawDB(t)
	defer rawDB.Close()

	if _, err := rawDB.Exec(
		`INSERT INTO session (id, title, directory, time_created, time_updated)
		 VALUES ('s-1', 'wt', '/src/.worktrees/foo/feat', 1000, 1000)`,
	); err != nil {
		t.Fatalf("seeding session: %v", err)
	}
	if err := srv.refreshProjectsIndex(); err != nil {
		t.Fatalf("projects refresh: %v", err)
	}

	// Archive using the folded root; the worktree-dir project should be
	// flagged archived because it folds to the same root.
	postProjectArchive(t, srv, "/src/foo", true)
	projects := getProjects(t, srv)
	if len(projects) != 1 || !projects[0].Archived {
		t.Fatalf("expected worktree project archived via folded root, got %+v", projects)
	}
}

func TestProjectArchive_MissingDirectory(t *testing.T) {
	srv := testServer(t)
	body, _ := json.Marshal(map[string]any{"directory": "", "archived": true})
	req := httptest.NewRequest(http.MethodPost, "/api/project/archive", strings.NewReader(string(body)))
	rr := httptest.NewRecorder()
	srv.handleProjectArchive(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty directory, got %d", rr.Code)
	}
}
