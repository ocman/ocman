package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
}

func (h *gitInfoOwner) RemoteID() string { return "rem1" }
func (h *gitInfoOwner) GitInfo(context.Context, []string) (map[string]git.Info, error) {
	h.calls++
	return map[string]git.Info{}, nil
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

func TestHandleGitDiff_MissingDir(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/git/diff", nil)
	rr := httptest.NewRecorder()
	srv.handleGitDiff(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHandleGitDiff_RelativeDirRejected(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/git/diff?dir=relative/path", nil)
	rr := httptest.NewRecorder()
	srv.handleGitDiff(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for relative dir, got %d", rr.Code)
	}
}

func TestHandleGitDiff_NotARepo(t *testing.T) {
	srv := testServer(t)
	dir := t.TempDir()
	req := httptest.NewRequest(http.MethodGet, "/api/git/diff?dir="+dir, nil)
	rr := httptest.NewRecorder()
	srv.handleGitDiff(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for non-repo, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHandleGitDiff_HappyPath(t *testing.T) {
	srv := testServer(t)
	dir := t.TempDir()
	gitInitForServerTest(t, dir)
	// Mutate so we have something to diff.
	if err := os.WriteFile(filepath.Join(dir, "foo.txt"), []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/git/diff?dir="+dir+"&fresh=1", nil)
	rr := httptest.NewRecorder()
	srv.handleGitDiff(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var got git.Diff
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v\nbody: %s", err, rr.Body.String())
	}
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

// TestHandleGitInfo_MissingDirs is the well-formedness check: callers
// must pass at least one `dirs` query parameter. Returning 400 here
// lets the frontend surface a clear error rather than silently
// receiving an empty map and pretending all is well.
func TestHandleGitInfo_MissingDirs(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/git/info", nil)
	rr := httptest.NewRecorder()
	srv.handleGitInfo(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing dirs, got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestHandleGitInfo_RelativeDirsRejected mirrors handleGitDiff: every
// path must be absolute so the server's CWD never enters into it.
// Even one relative path in the comma-separated list rejects the
// whole request — partial trust would be confusing.
func TestHandleGitInfo_RelativeDirsRejected(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/git/info?dirs=/abs/ok,relative/bad", nil)
	rr := httptest.NewRecorder()
	srv.handleGitInfo(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for relative dir, got %d", rr.Code)
	}
}

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

	req := httptest.NewRequest(http.MethodGet, "/api/git/info?dirs="+a+","+b, nil)
	rr := httptest.NewRecorder()
	srv.handleGitInfo(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var got map[string]git.Info
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v\nbody: %s", err, rr.Body.String())
	}
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

	req := httptest.NewRequest(http.MethodGet, "/api/git/info?dirs="+dir, nil)
	rr := httptest.NewRecorder()
	srv.handleGitInfo(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var got map[string]git.Info
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if info, ok := got[dir]; ok && info.IsRepo() {
		t.Errorf("non-repo dir reported as repo: %+v", info)
	}
}

func TestHandleGitInfo_UsesExplicitRemoteOwner(t *testing.T) {
	srv := testServer(t)
	host := &gitInfoOwner{}
	srv.hostRouter = hostsvc.NewRouter(&ownerSpy{})
	srv.hostRouter.RegisterRemote("rem1", host)

	rr := httptest.NewRecorder()
	srv.handleGitInfo(rr, httptest.NewRequest(http.MethodGet, "/api/git/info?dirs=/remote/repo&remoteId=rem1", nil))
	if rr.Code != http.StatusOK || host.calls != 1 {
		t.Fatalf("status = %d, owner calls = %d", rr.Code, host.calls)
	}
}

func TestHandleGitInfo_RejectsUnknownExplicitOwner(t *testing.T) {
	srv := testServer(t)
	rr := httptest.NewRecorder()
	srv.handleGitInfo(rr, httptest.NewRequest(http.MethodGet, "/api/git/info?dirs=/remote/repo&remoteId=gone", nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
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

func TestHandleGitBranches_MissingDir(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/git/branches", nil)
	rr := httptest.NewRecorder()
	srv.handleGitBranches(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestHandleGitBranches_RelativeRejected(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/git/branches?dir=rel/path", nil)
	rr := httptest.NewRecorder()
	srv.handleGitBranches(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestHandleGitBranches_HappyPath(t *testing.T) {
	srv := testServer(t)
	dir := t.TempDir()
	gitInitForServerTest(t, dir)
	gitRunForServerTest(t, dir, "branch", "feature/x")

	req := httptest.NewRequest(http.MethodGet, "/api/git/branches?dir="+dir, nil)
	rr := httptest.NewRecorder()
	srv.handleGitBranches(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var got struct {
		Branches []string `json:"branches"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(got.Branches) != 2 {
		t.Fatalf("branches = %v, want 2", got.Branches)
	}
	if got.Branches[0] != "main" {
		t.Errorf("current branch not first: %v", got.Branches)
	}
}

// --- /api/git/checkout ---

func TestHandleGitCheckout_BadBody(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/git/checkout", strings.NewReader("{"))
	rr := httptest.NewRecorder()
	srv.handleGitCheckout(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestHandleGitCheckout_MissingFields(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/git/checkout", strings.NewReader(`{"dir":"/x"}`))
	rr := httptest.NewRecorder()
	srv.handleGitCheckout(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing branch, got %d", rr.Code)
	}
}

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
