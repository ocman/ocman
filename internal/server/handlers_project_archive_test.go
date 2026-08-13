package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/state"
)

type projectArchiveHost struct {
	hostsvc.Host
	remoteID string
	stopped string
	stopErr error
}

func (h *projectArchiveHost) RemoteID() string {
	if h.remoteID != "" {
		return h.remoteID
	}
	return state.LocalRemoteID
}

func (h *projectArchiveHost) StopProjectOpencode(_ context.Context, req hostsvc.EnsureProjectOpencodeRequest) error {
	h.stopped = req.ProjectDir
	return h.stopErr
}

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

func TestProjectArchive_StopsProjectOpencode(t *testing.T) {
	srv := testServer(t)
	host := &projectArchiveHost{}
	srv.hostRouter = hostsvc.NewRouter(host)

	postProjectArchive(t, srv, "/src/.worktrees/foo/feat", true)

	if host.stopped != "/src/foo" {
		t.Fatalf("stopped project = %q, want /src/foo", host.stopped)
	}
}

// TestProjectArchive_SucceedsWhenStopFails is the regression for archive
// silently failing: stopping the managed opencode is best-effort (a dead
// tmux session, a removed directory or an unreachable remote all error),
// so it must not fail the archive itself.
func TestProjectArchive_SucceedsWhenStopFails(t *testing.T) {
	srv := testServer(t)
	srv.hostRouter = hostsvc.NewRouter(&projectArchiveHost{stopErr: errors.New("can't find session")})

	postProjectArchive(t, srv, "/src/foo", true)

	archived, _ := srv.stateDB.ArchivedProjects()
	if _, ok := archived[state.ProjectKey{RemoteID: state.LocalRemoteID, Root: "/src/foo"}]; !ok {
		t.Fatalf("expected /src/foo archived despite stop error, got %v", archived)
	}
}

// TestProjectArchive_AppliesToRemoteProjects verifies remote projects
// honour archive markers. Regression: remote projects used to bypass the
// archive pipeline entirely and re-appeared on every refresh. Archiving a
// remote-tagged project must set Archived; newer activity auto-unarchives.
func TestProjectArchive_AppliesToRemoteProjects(t *testing.T) {
	srv, rawDB := testServerWithRawDB(t)
	defer rawDB.Close()
	srv.router().RegisterRemote("r1", &projectArchiveHost{remoteID: "r1"})

	// Inject a remote project the way handleProjects appends them.
	remoteLastUsed := int64(100)
	srv.remoteProjectsFn = func() []db.ProjectStats {
		return []db.ProjectStats{
			{Directory: "/remote/repo", RemoteID: "r1", RemoteName: "box", LastUsed: remoteLastUsed},
		}
	}

	// The client names the owning host: the hub cannot tell from the path
	// alone which machine /remote/repo lives on.
	postProjectArchiveOn(t, srv, "r1", "/remote/repo", true)

	// Through the full handler: the remote project must be flagged
	// archived (pre-fix it bypassed applyProjectArchiveState and stayed
	// visible, re-appearing on every refresh).
	projects := getProjects(t, srv)
	if len(projects) != 1 || !projects[0].Archived {
		t.Fatalf("expected remote project archived, got %+v", projects)
	}

	// Newer activity than archived_at auto-unarchives — most recent wins.
	remoteLastUsed = 9999999999999
	projects = getProjects(t, srv)
	if len(projects) != 1 || projects[0].Archived {
		t.Fatalf("expected remote project auto-unarchived after newer activity, got %+v", projects)
	}
	if archived, _ := srv.stateDB.ArchivedProjects(); len(archived) != 0 {
		t.Errorf("expected archive marker deleted, got %v", archived)
	}
}

func postProjectArchiveOn(t *testing.T, srv *Server, remoteID, dir string, archived bool) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"directory": dir, "archived": archived, "remoteId": remoteID})
	req := httptest.NewRequest(http.MethodPost, "/api/project/archive", strings.NewReader(string(body)))
	rr := httptest.NewRecorder()
	srv.handleProjectArchive(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("archive %s:%q=%v: expected 200, got %d: %s", remoteID, dir, archived, rr.Code, rr.Body.String())
	}
}

// A project's identity is (remoteID, root). The same absolute path exists on
// several machines, so archiving one machine's copy must not hide the other's
// — and activity on one must not auto-unarchive the other.
func TestProjectArchive_IsPerHost(t *testing.T) {
	srv, rawDB := testServerWithRawDB(t)
	defer rawDB.Close()
	srv.router().RegisterRemote("r1", &projectArchiveHost{remoteID: "r1"})

	const shared = "/repo/shared"
	if _, err := rawDB.Exec(
		`INSERT INTO session (id, title, directory, time_created, time_updated)
		 VALUES ('s-1', 'local', '`+shared+`', 1000, 1000)`,
	); err != nil {
		t.Fatalf("seeding session: %v", err)
	}
	if err := srv.refreshProjectsIndex(); err != nil {
		t.Fatalf("projects refresh: %v", err)
	}
	remoteLastUsed := int64(100)
	srv.remoteProjectsFn = func() []db.ProjectStats {
		return []db.ProjectStats{{Directory: shared, RemoteID: "r1", RemoteName: "box", LastUsed: remoteLastUsed}}
	}

	// Archive the remote's copy only.
	postProjectArchiveOn(t, srv, "r1", shared, true)

	byRemote := map[string]db.ProjectStats{}
	for _, p := range getProjects(t, srv) {
		byRemote[p.RemoteID] = p
	}
	if len(byRemote) != 2 {
		t.Fatalf("projects = %+v, want one row per host", byRemote)
	}
	if !byRemote["r1"].Archived {
		t.Fatalf("remote project = %+v, want archived", byRemote["r1"])
	}
	if byRemote[""].Archived {
		t.Fatalf("local project = %+v, want untouched by the remote's archive", byRemote[""])
	}

	// Fresh activity on the LOCAL copy must not auto-unarchive the remote's.
	if _, err := rawDB.Exec(`UPDATE session SET time_updated = 9999999999999 WHERE id = 's-1'`); err != nil {
		t.Fatalf("bumping activity: %v", err)
	}
	if err := srv.refreshProjectsIndex(); err != nil {
		t.Fatalf("projects refresh 2: %v", err)
	}
	byRemote = map[string]db.ProjectStats{}
	for _, p := range getProjects(t, srv) {
		byRemote[p.RemoteID] = p
	}
	if !byRemote["r1"].Archived {
		t.Fatalf("remote project = %+v, want still archived: the activity was on another host", byRemote["r1"])
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

func TestProjectArchive_RejectsDisconnectedRemote(t *testing.T) {
	srv := testServer(t)
	body, _ := json.Marshal(map[string]any{"directory": "/remote/repo", "archived": true, "remoteId": "r1"})
	req := httptest.NewRequest(http.MethodPost, "/api/project/archive", strings.NewReader(string(body)))
	rr := httptest.NewRecorder()
	srv.handleProjectArchive(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 for disconnected remote, got %d: %s", rr.Code, rr.Body.String())
	}
}
