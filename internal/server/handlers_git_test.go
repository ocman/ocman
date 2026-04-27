package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/gitinfo"
)

// gitInitForServerTest is a copy of the helper in the gitinfo package's
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
		cmd.Env = append(os.Environ(),
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
	req := httptest.NewRequest("GET", "/api/git/diff", nil)
	rr := httptest.NewRecorder()
	srv.handleGitDiff(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHandleGitDiff_RelativeDirRejected(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest("GET", "/api/git/diff?dir=relative/path", nil)
	rr := httptest.NewRecorder()
	srv.handleGitDiff(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for relative dir, got %d", rr.Code)
	}
}

func TestHandleGitDiff_NotARepo(t *testing.T) {
	srv := testServer(t)
	dir := t.TempDir()
	req := httptest.NewRequest("GET", "/api/git/diff?dir="+dir, nil)
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

	req := httptest.NewRequest("GET", "/api/git/diff?dir="+dir+"&fresh=1", nil)
	rr := httptest.NewRecorder()
	srv.handleGitDiff(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var got gitinfo.Diff
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
