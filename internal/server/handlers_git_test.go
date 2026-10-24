package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/git"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
)

type gitInfoOwner struct {
	hostsvc.Host
	calls int
	dirs  []string
}

func (h *gitInfoOwner) RemoteID() string { return "rem1" }
func (h *gitInfoOwner) GitInfo(_ context.Context, dirs []string) (map[string]git.Info, error) {
	h.calls++
	h.dirs = dirs
	return map[string]git.Info{}, nil
}

func TestHandleGitInfo_LiteralDirectory(t *testing.T) {
	for _, dir := range []string{"/repo/foo,bar", "/repo/ spaced "} {
		t.Run(dir, func(t *testing.T) {
			host := &gitInfoOwner{}
			srv := &Server{hostRouter: hostsvc.NewRouter(&ownerSpy{})}
			srv.hostRouter.RegisterRemote("rem1", host)
			query := url.Values{"dir": {dir}, "remoteId": {"rem1"}}
			w := httptest.NewRecorder()
			srv.handleGitInfo(w, httptest.NewRequest(http.MethodGet, "/api/git/info?"+query.Encode(), nil))
			if w.Code != http.StatusOK || len(host.dirs) != 1 || host.dirs[0] != dir {
				t.Fatalf("status %d, owner directories %q; want one literal directory %q", w.Code, host.dirs, dir)
			}
		})
	}
}

// Every git route rejects invalid input, and the diff of a non-repo is 404. Relative paths are rejected whether passed as the literal
// `dir` or inside the comma-separated `dirs` list.
func TestGitRoutesRejectInvalidInput(t *testing.T) {
	srv := testServer(t)
	notRepo := t.TempDir()
	for _, tc := range []struct {
		method, target, body string
		want                 int
	}{
		{http.MethodGet, "/api/git/info", "", http.StatusBadRequest},
		{http.MethodGet, "/api/git/info?dir=relative,dir&remoteId=local", "", http.StatusBadRequest},
		{http.MethodGet, "/api/git/info?dirs=/abs/ok,relative/bad", "", http.StatusBadRequest},
		{http.MethodGet, "/api/git/info?dirs=/remote/repo", "", http.StatusBadRequest},
		{http.MethodGet, "/api/git/info?dirs=/remote/repo&remoteId=gone", "", http.StatusServiceUnavailable},
		{http.MethodGet, "/api/git/diff", "", http.StatusBadRequest},
		{http.MethodGet, "/api/git/diff?dir=relative/path", "", http.StatusBadRequest},
		{http.MethodGet, "/api/git/diff?dir=" + notRepo, "", http.StatusNotFound},
		{http.MethodGet, "/api/git/branches", "", http.StatusBadRequest},
		{http.MethodGet, "/api/git/branches?dir=rel/path", "", http.StatusBadRequest},
		{http.MethodPost, "/api/git/checkout", "{", http.StatusBadRequest},
		{http.MethodPost, "/api/git/checkout", `{"dir":"/x"}`, http.StatusBadRequest},
	} {
		t.Run(tc.method+" "+tc.target+" "+tc.body, func(t *testing.T) {
			rr := httptest.NewRecorder()
			srv.serveGit(rr, httptest.NewRequest(tc.method, tc.target, strings.NewReader(tc.body)))
			if rr.Code != tc.want {
				t.Fatalf("status = %d, want %d, body = %s", rr.Code, tc.want, rr.Body.String())
			}
		})
	}
}

// gitInitForServerTest is a copy of the helper in the git package's
// test, scoped to this package so we don't depend on internal test
// helpers across packages. Skips the test when git isn't available.
func gitInitForServerTest(t *testing.T, dir string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(cleanGitEnvForTest(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test",
			"GIT_CONFIG_GLOBAL=/dev/null",
			"GIT_CONFIG_SYSTEM=/dev/null",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "--initial-branch=main")
	run("config", "user.email", "test@test")
	run("config", "user.name", "test")
	run("config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(dir, "foo.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write foo.txt: %v", err)
	}
	run("add", "foo.txt")
	run("commit", "-m", "init")
}

func TestHandleGitDiff_HappyPath(t *testing.T) {
	srv := testServer(t)
	dir := t.TempDir()
	gitInitForServerTest(t, dir)
	// Mutate so we have something to diff.
	if err := os.WriteFile(filepath.Join(dir, "foo.txt"), []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	got := getJSON[git.Diff](t, srv.handleGitDiff, "/api/git/diff?dir="+dir+"&fresh=1")
	if got.Branch != "main" {
		t.Errorf("Branch = %q, want main", got.Branch)
	}
	if len(got.Files) != 1 || got.Files[0].Path != "foo.txt" {
		t.Errorf("Files = %+v", got.Files)
	}
	if got.Files[0].Status != "modified" {
		t.Errorf("Status = %q, want modified", got.Files[0].Status)
	}
}

// --- /api/git/info ---

// TestHandleGitInfo_HappyPath spins up two real git worktrees in
// temp dirs and confirms the handler returns Info for each, keyed by
// directory. We don't assert exact branch/dirty contents — those
// belong to the git package's own tests — only that the wiring
// works end-to-end.
func TestHandleGitInfo_HappyPath(t *testing.T) {
	srv := testServer(t)
	a := t.TempDir()
	b := t.TempDir()
	gitInitForServerTest(t, a)
	gitInitForServerTest(t, b)

	got := getJSON[map[string]git.Info](t, srv.handleGitInfo, "/api/git/info?dirs="+a+","+b+"&remoteId=local")
	if _, ok := got[a]; !ok {
		t.Errorf("missing entry for %s in %v", a, got)
	}
	if _, ok := got[b]; !ok {
		t.Errorf("missing entry for %s in %v", b, got)
	}
	if branch := got[a].Branch; branch != "main" {
		t.Errorf("a.Branch = %q, want main", branch)
	}
}

// TestHandleGitInfo_NonRepoDirs returns the dir as a key with a zero
// Info. The frontend treats Info{Branch: ""} as "not a repo" and
// renders nothing — a missing key would also work but this is more
// explicit and matches the git.LookupMany contract.
func TestHandleGitInfo_NonRepoDirs(t *testing.T) {
	srv := testServer(t)
	dir := t.TempDir() // no git init

	got := getJSON[map[string]git.Info](t, srv.handleGitInfo, "/api/git/info?dirs="+dir+"&remoteId=local")
	if info, ok := got[dir]; ok && info.IsRepo() {
		t.Errorf("non-repo dir reported as repo: %+v", info)
	}
}

func TestHandleGitInfo_UsesExplicitRemoteOwner(t *testing.T) {
	srv := testServer(t)
	host := &gitInfoOwner{}
	withRemoteOwner(srv, host)

	rr := httptest.NewRecorder()
	srv.handleGitInfo(rr, httptest.NewRequest(http.MethodGet, "/api/git/info?dirs=/remote/repo&remoteId=rem1", nil))
	if rr.Code != http.StatusOK || host.calls != 1 {
		t.Fatalf("status = %d, owner calls = %d", rr.Code, host.calls)
	}
}

// --- /api/git/branches ---

func gitRunForServerTest(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(cleanGitEnvForTest(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestHandleGitBranches_HappyPath(t *testing.T) {
	srv := testServer(t)
	dir := t.TempDir()
	gitInitForServerTest(t, dir)
	gitRunForServerTest(t, dir, "branch", "feature/x")

	got := getJSON[struct {
		Branches []string `json:"branches"`
	}](t, srv.handleGitBranches, "/api/git/branches?dir="+dir)
	if len(got.Branches) != 2 {
		t.Fatalf("branches = %v, want 2", got.Branches)
	}
	if got.Branches[0] != "main" {
		t.Errorf("current branch not first: %v", got.Branches)
	}
}

// --- /api/git/checkout ---

func TestHandleGitCheckout_HappyPath(t *testing.T) {
	srv := testServer(t)
	dir := t.TempDir()
	gitInitForServerTest(t, dir)
	gitRunForServerTest(t, dir, "branch", "other")

	body := `{"dir":"` + dir + `","branch":"other"}`
	req := httptest.NewRequest(http.MethodPost, "/api/git/checkout", strings.NewReader(body))
	rr := httptest.NewRecorder()
	srv.handleGitCheckout(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if b := git.Lookup(req.Context(), dir).Branch; b != "other" {
		t.Errorf("branch after checkout = %q, want other", b)
	}
}

func TestHandleGitCheckout_DirtyConflict(t *testing.T) {
	srv := testServer(t)
	dir := t.TempDir()
	gitInitForServerTest(t, dir)
	gitRunForServerTest(t, dir, "checkout", "-b", "other")
	if err := os.WriteFile(filepath.Join(dir, "foo.txt"), []byte("other\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRunForServerTest(t, dir, "commit", "-am", "other")
	gitRunForServerTest(t, dir, "checkout", "main")
	// Dirty main so the switch is refused.
	if err := os.WriteFile(filepath.Join(dir, "foo.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	body := `{"dir":"` + dir + `","branch":"other"}`
	req := httptest.NewRequest(http.MethodPost, "/api/git/checkout", strings.NewReader(body))
	rr := httptest.NewRecorder()
	srv.handleGitCheckout(rr, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rr.Code, rr.Body.String())
	}
}
