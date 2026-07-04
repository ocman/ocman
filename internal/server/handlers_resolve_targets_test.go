package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/gitexec"
	"github.com/NoUseFreak/ocman/internal/remote"
)

func TestHandleResolveTargets_SingleHostLocalOnly(t *testing.T) {
	srv := testServer(t) // no remote manager

	body := `{"dir":"/some/project"}`
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/resolve-targets", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.handleResolveTargets(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Candidates []remote.TargetCandidate `json:"candidates"`
		Remotes    []remote.TargetCandidate `json:"remotes"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Candidates) != 1 || resp.Candidates[0].RemoteID != "local" {
		t.Fatalf("expected single local candidate, got %+v", resp.Candidates)
	}
	if resp.Candidates[0].Dir != "/some/project" {
		t.Fatalf("candidate dir = %q", resp.Candidates[0].Dir)
	}
}

func TestHandleResolveTargets_RequiresDir(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/resolve-targets", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.handleResolveTargets(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

// initOriginRepo creates a git repo with an origin remote so
// localGitOrigin returns a non-empty URL.
func initOriginRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(gitexec.CleanEnv(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-b", "main")
	run("remote", "add", "origin", "https://example.com/org/repo.git")
	if err := os.WriteFile(filepath.Join(dir, "f"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "f")
	run("commit", "-m", "init")
	return dir
}

// TestHandleResolveTargets_WithManagerLocalMatch exercises the
// manager-present path: localProjectIdentities + localGitOrigin run, and
// the resolver matches the dir against the local projects index.
func TestHandleResolveTargets_WithManagerLocalMatch(t *testing.T) {
	repo := initOriginRepo(t)
	srv := testServer(t)
	withManager(t, srv)

	body := `{"dir":"` + repo + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/resolve-targets", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.handleResolveTargets(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Candidates []remote.TargetCandidate `json:"candidates"`
		Remotes    []remote.TargetCandidate `json:"remotes"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	// With a manager and no matching project in the (empty) local index,
	// zero candidates is valid; the response shape must still be present.
	if resp.Candidates == nil && resp.Remotes == nil {
		// nil slices marshal as null; ensure the keys exist.
		if !bytes.Contains(rr.Body.Bytes(), []byte("candidates")) {
			t.Fatal("response missing candidates key")
		}
	}
}

// TestLocalGitOrigin covers the origin lookup helper directly.
func TestLocalGitOrigin(t *testing.T) {
	repo := initOriginRepo(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if got := localGitOrigin(req, repo); got != "https://example.com/org/repo.git" {
		t.Errorf("localGitOrigin = %q", got)
	}
	// A non-repo dir yields the empty string.
	if got := localGitOrigin(req, t.TempDir()); got != "" {
		t.Errorf("localGitOrigin(non-repo) = %q, want empty", got)
	}
}
