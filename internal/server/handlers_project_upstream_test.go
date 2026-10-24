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

	"github.com/NoUseFreak/ocman/internal/forge"
	"github.com/NoUseFreak/ocman/internal/forge/github"
	"github.com/NoUseFreak/ocman/internal/git"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
)

// initGitHubRepo creates a local git repo whose origin remote points
// at a github.com URL — enough for upstream-detection to classify
// it as a GitHub remote. The remote URL is bogus (we never fetch
// from it); the project upstream endpoints only inspect the remote
// list, not the contents.
func initGitHubRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = cleanGitEnvForTest()
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "README")
	cmd := exec.Command("git", "commit", "-m", "initial")
	cmd.Dir = dir
	cmd.Env = append(cleanGitEnvForTest(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}
	run("remote", "add", "origin", "https://github.com/alice/myproj.git")
	return dir
}

func TestHandleProjectUpstreams_DetectsGitHubRemote(t *testing.T) {
	srv := testServer(t)
	dir := initGitHubRepo(t)

	req := httptest.NewRequest(http.MethodGet, "/api/project/upstreams?dir="+dir+"&remoteId=local", nil)
	rr := httptest.NewRecorder()
	serveProjectForge(srv, rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Upstreams []forge.Remote `json:"upstreams"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, rr.Body.String())
	}
	if len(resp.Upstreams) != 1 {
		t.Fatalf("expected 1 upstream, got %d", len(resp.Upstreams))
	}
	u := resp.Upstreams[0]
	if u.Name != "origin" || u.Type != forge.RemoteTypeGitHub || u.Repo != "alice/myproj" {
		t.Errorf("unexpected upstream: %+v", u)
	}
}

func TestHandleProjectUpstreams_UsesExplicitRemoteOwnerForRemoteOnlyPath(t *testing.T) {
	srv := testServer(t)
	host := &projectHandleRemoteHost{upstreams: githubProjectUpstreams("/remote/only/repo")}
	withRemoteOwner(srv, host)

	rr := httptest.NewRecorder()
	serveProjectForge(srv, rr, httptest.NewRequest(http.MethodGet, "/api/project/upstreams?dir=/remote/only/repo&remoteId=rem1", nil))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"repo":"alice/myproj"`) {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
}

func TestProjectUpstreams_ClassifiesForgejoWithHubConfiguration(t *testing.T) {
	srv := testServer(t)
	registerForgejoClient(srv, "code.example.com", "https://code.example.com")
	host := &projectHandleRemoteHost{upstreams: &hostsvc.ProjectUpstreams{
		RepoRoot: "/remote/repo",
		Remotes:  []forge.Remote{{Name: "origin", Host: "code.example.com", Repo: "owner/repo"}},
	}}
	withRemoteOwner(srv, host)

	rr := httptest.NewRecorder()
	serveProjectForge(srv, rr, httptest.NewRequest(http.MethodGet, "/api/project/upstreams?dir=/remote/repo&remoteId=rem1", nil))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"type":"forgejo"`) {
		t.Fatalf("status = %d, body = %s, want hub-classified Forgejo remote", rr.Code, rr.Body.String())
	}
}

func TestHostProjectUpstreamsDoesNotDependOnOwnerForgeCredentials(t *testing.T) {
	dir := t.TempDir()
	gitInitForServerTest(t, dir)
	gitRunForServerTest(t, dir, "remote", "add", "origin", "https://code.example.com/owner/repo.git")

	upstreams, err := (&Server{}).hostProjectUpstreams(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(upstreams.Remotes) != 1 || upstreams.Remotes[0].Host != "code.example.com" {
		t.Fatalf("remotes = %+v, want credential-independent candidate", upstreams.Remotes)
	}
}

func TestHostFetchPRHeadDoesNotDependOnOwnerForgeCredentials(t *testing.T) {
	dir := t.TempDir()
	gitInitForServerTest(t, dir)
	gitRunForServerTest(t, dir, "remote", "add", "origin", "ssh://127.0.0.1:1/owner/repo.git")

	_, err := (&Server{}).hostFetchPRHead(context.Background(), hostsvc.FetchPRHeadRequest{
		RepoRoot: dir, Remote: "origin", Number: 7,
	})
	if err == nil || strings.Contains(err.Error(), "forge client") {
		t.Fatalf("error = %v, want owner-local git fetch attempt", err)
	}
}

func TestHandleProjectUpstreams_RemoteNotARepoReturns404(t *testing.T) {
	srv := testServer(t)
	host := &projectHandleRemoteHost{upstreamErr: git.ErrNotARepo}
	withRemoteOwner(srv, host)
	rr := httptest.NewRecorder()
	serveProjectForge(srv, rr, httptest.NewRequest(http.MethodGet, "/api/project/upstreams?dir=/remote/nope&remoteId=rem1", nil))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
}

func TestHandleProjectUpstreams_RejectsInvalidOwnerResult(t *testing.T) {
	srv := testServer(t)
	host := &projectHandleRemoteHost{upstreams: githubProjectUpstreams("relative")}
	withRemoteOwner(srv, host)
	rr := httptest.NewRecorder()
	serveProjectForge(srv, rr, httptest.NewRequest(http.MethodGet, "/api/project/upstreams?dir=/repo&remoteId=rem1", nil))
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
}

func TestProjectUpstreamEndpointsRequireExplicitOwnerAndAbsoluteDir(t *testing.T) {
	srv := testServer(t)
	tests := []string{
		"/api/project/upstreams?dir=/repo",
		"/api/project/upstreams?dir=relative/path",
		"/api/project/prs?dir=/repo&remote=origin",
		"/api/project/issues?dir=/repo&remote=origin",
		"/api/project/forge-user?dir=/repo&remote=origin",
		"/api/project/pr-checks?dir=/repo&remote=origin&sha=abc",
		"/api/project/prs?dir=relative&remote=origin&remoteId=local",
		"/api/project/issues?dir=relative&remote=origin&remoteId=local",
		"/api/project/forge-user?dir=relative&remote=origin&remoteId=local",
		"/api/project/pr-checks?dir=relative&remote=origin&remoteId=local&sha=abc",
		"/api/project/pr-checks?dir=/repo&remote=origin&remoteId=local&sha=../user",
	}
	for _, target := range tests {
		t.Run(target, func(t *testing.T) {
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, target, nil)
			serveProjectForge(srv, rr, req)
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestHandleProjectUpstreams_404WhenNotARepo(t *testing.T) {
	srv := testServer(t)
	notARepo := t.TempDir()
	req := httptest.NewRequest(http.MethodGet, "/api/project/upstreams?dir="+notARepo+"&remoteId=local", nil)
	rr := httptest.NewRecorder()
	serveProjectForge(srv, rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandleProjectUpstreams_EmptyWhenNoSupportedRemotes(t *testing.T) {
	srv := testServer(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = cleanGitEnvForTest()
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-b", "main")
	// No remotes at all — should produce []
	req := httptest.NewRequest(http.MethodGet, "/api/project/upstreams?dir="+dir+"&remoteId=local", nil)
	rr := httptest.NewRecorder()
	serveProjectForge(srv, rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	body := strings.TrimSpace(rr.Body.String())
	if !strings.Contains(body, `"upstreams":[]`) {
		t.Errorf("expected empty upstreams array, got %s", body)
	}
}

// fakeGitHubServer mounts the minimal endpoints needed to test the
// PR/Issue list handlers. Returns the httptest server plus a
// github.Client pointed at it; the caller stuffs the client into
// srv.integrations.GitHub.
func fakeGitHubServer(t *testing.T) (*httptest.Server, *github.Client) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/pulls/42"):
			_, _ = w.Write([]byte(`{
				"number":42,"title":"Patch","body":"body","state":"open","html_url":"https://github.com/alice/myproj/pull/42",
				"user":{"login":"alice"},"head":{"ref":"patch","repo":{"full_name":"alice/myproj"}},"base":{"repo":{"full_name":"alice/myproj"}}
			}`))
		case strings.HasSuffix(r.URL.Path, "/issues/7"):
			_, _ = w.Write([]byte(`{
				"number":7,"title":"Bug report","body":"broken","state":"open","html_url":"https://github.com/alice/myproj/issues/7","user":{"login":"carol"}
			}`))
		case strings.HasPrefix(r.URL.Path, "/repos/") && strings.HasSuffix(r.URL.Path, "/pulls"):
			_, _ = w.Write([]byte(`[{
				"number": 42, "title": "Patch", "body": "body",
				"state": "open", "draft": false,
				"updated_at": "2026-05-21T14:03:11Z",
				"html_url": "https://github.com/alice/myproj/pull/42",
				"user": {"login": "alice"},
				"labels": [], "assignees": [{"login": "alice"}],
				"requested_reviewers": [{"login": "bob"}],
				"head": {"ref": "patch", "repo": {"full_name": "alice/myproj"}},
				"base": {"repo": {"full_name": "alice/myproj"}}
			}]`))
		case strings.HasPrefix(r.URL.Path, "/repos/") && strings.HasSuffix(r.URL.Path, "/issues"):
			_, _ = w.Write([]byte(`[{
				"number": 7, "title": "Bug report", "body": "broken",
				"state": "open",
				"updated_at": "2026-05-21T14:03:11Z",
				"html_url": "https://github.com/alice/myproj/issues/7",
				"user": {"login": "carol"},
				"labels": [], "assignees": [{"login": "alice"}]
			}]`))
		case r.URL.Path == "/user":
			_, _ = w.Write([]byte(`{"login": "alice"}`))
		case strings.HasSuffix(r.URL.Path, "/status"):
			// Legacy combined commit status.
			_, _ = w.Write([]byte(`{"statuses":[{"state":"success","context":"ci/lint","target_url":"https://ci/lint"}]}`))
		case strings.HasSuffix(r.URL.Path, "/check-runs"):
			_, _ = w.Write([]byte(`{"check_runs":[{"name":"build","status":"completed","conclusion":"success","html_url":"https://gh/build"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	client := github.NewWithCredentials(srv.URL, "test-token", srv.Client())
	return srv, client
}

func TestProjectForgeEndpointsRequireConfiguredClient(t *testing.T) {
	srv := testServer(t)
	srv.integrations.GitHub = nil
	dir := initGitHubRepo(t)
	tests := []struct {
		path string
		run  func(http.ResponseWriter, *http.Request)
	}{
		{"/api/project/prs?dir=" + dir + "&remoteId=local&remote=origin", projectForgeRoute(srv)},
		{"/api/project/issues?dir=" + dir + "&remoteId=local&remote=origin", projectForgeRoute(srv)},
		{"/api/project/forge-user?dir=" + dir + "&remoteId=local&remote=origin", projectForgeRoute(srv)},
		{"/api/project/pr-checks?dir=" + dir + "&remoteId=local&remote=origin&sha=abc", projectForgeRoute(srv)},
	}
	for _, tt := range tests {
		rr := httptest.NewRecorder()
		tt.run(rr, httptest.NewRequest(http.MethodGet, tt.path, nil))
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("%s: status = %d, body = %s", tt.path, rr.Code, rr.Body.String())
		}
	}
}

func TestHandleProjectPRChecks_NormalisesNilChecksToEmptyArray(t *testing.T) {
	srv := testServer(t)
	dir := initGitHubRepo(t)

	// A forge with no CI configured: both endpoints return 404, so the
	// GitHub adapter rolls up to CIStateUnknown with a nil check slice.
	// The handler must emit "checks":[] (never null) for the frontend.
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer gh.Close()
	srv.integrations.GitHub = github.NewWithCredentials(gh.URL, "test-token", gh.Client())

	req := httptest.NewRequest(http.MethodGet,
		"/api/project/pr-checks?dir="+dir+"&remoteId=local&remote=origin&sha=abc123", nil)
	rr := httptest.NewRecorder()
	serveProjectForge(srv, rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	// Assert the raw JSON contains an empty array, not null, so the
	// frontend can map over it unconditionally.
	if !strings.Contains(rr.Body.String(), `"checks":[]`) {
		t.Errorf("expected checks:[] in body, got %s", rr.Body.String())
	}
	var resp struct {
		State  forge.CIState `json:"state"`
		Checks []forge.Check `json:"checks"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, rr.Body.String())
	}
	if resp.State != forge.CIStateUnknown {
		t.Errorf("state: got %q want unknown", resp.State)
	}
	if resp.Checks == nil {
		t.Errorf("checks should be a non-nil empty slice")
	}
}

// The read-only forge endpoints share one repo and fake GitHub server;
// no subtest mutates server or forge state.
func TestProjectForgeReadEndpoints(t *testing.T) {
	srv := testServer(t)
	dir := initGitHubRepo(t)
	gh, ghc := fakeGitHubServer(t)
	defer gh.Close()
	srv.integrations.GitHub = ghc
	base := "?dir=" + dir + "&remoteId=local&remote="
	get := func(t *testing.T, target string, want int, out any) {
		t.Helper()
		rr := httptest.NewRecorder()
		serveProjectForge(srv, rr, httptest.NewRequest(http.MethodGet, target, nil))
		if rr.Code != want {
			t.Fatalf("%s: status = %d, want %d, body = %s", target, rr.Code, want, rr.Body.String())
		}
		if out != nil {
			if err := json.Unmarshal(rr.Body.Bytes(), out); err != nil {
				t.Fatalf("decode: %v body=%s", err, rr.Body.String())
			}
		}
	}

	t.Run("prs", func(t *testing.T) {
		var resp struct {
			PRs []forge.PR `json:"prs"`
		}
		get(t, "/api/project/prs"+base+"origin&state=open", http.StatusOK, &resp)
		if len(resp.PRs) != 1 || resp.PRs[0].Number != 42 {
			t.Errorf("unexpected prs: %+v", resp.PRs)
		}
	})
	t.Run("prs mine matches author, assignee or reviewer", func(t *testing.T) {
		for login, want := range map[string]int{"alice": 1, "bob": 1, "stranger": 0} {
			var resp struct {
				PRs []forge.PR `json:"prs"`
			}
			get(t, "/api/project/prs"+base+"origin&mine="+login, http.StatusOK, &resp)
			if len(resp.PRs) != want {
				t.Errorf("login %s: got %d prs, want %d", login, len(resp.PRs), want)
			}
		}
	})
	t.Run("issues", func(t *testing.T) {
		var resp struct {
			Issues []forge.Issue `json:"issues"`
		}
		get(t, "/api/project/issues"+base+"origin", http.StatusOK, &resp)
		if len(resp.Issues) != 1 || resp.Issues[0].Number != 7 {
			t.Errorf("unexpected issues: %+v", resp.Issues)
		}
	})
	t.Run("issues mine filters assignees", func(t *testing.T) {
		for login, want := range map[string]int{"alice": 1, "stranger": 0} {
			var resp struct {
				Issues []forge.Issue `json:"issues"`
			}
			get(t, "/api/project/issues"+base+"origin&mine="+login, http.StatusOK, &resp)
			if len(resp.Issues) != want {
				t.Errorf("login %s: got %d issues, want %d", login, len(resp.Issues), want)
			}
		}
	})
	t.Run("forge user", func(t *testing.T) {
		var u forge.CurrentUser
		get(t, "/api/project/forge-user"+base+"origin", http.StatusOK, &u)
		if u.Login != "alice" || u.Host != "github.com" {
			t.Errorf("got %+v", u)
		}
	})
	t.Run("pr checks roll up", func(t *testing.T) {
		var resp struct {
			State  forge.CIState `json:"state"`
			Checks []forge.Check `json:"checks"`
		}
		get(t, "/api/project/pr-checks"+base+"origin&sha=abc123", http.StatusOK, &resp)
		if resp.State != forge.CIStateSuccess {
			t.Errorf("state: got %q want success", resp.State)
		}
		if len(resp.Checks) != 2 {
			t.Errorf("expected 2 checks (status + check-run), got %d", len(resp.Checks))
		}
	})
	t.Run("rejections", func(t *testing.T) {
		for _, tc := range []struct {
			target string
			want   int
		}{
			{"/api/project/prs" + base + "nonexistent", http.StatusNotFound},
			{"/api/project/prs" + base + "origin&state=invalid", http.StatusBadRequest},
			{"/api/project/pr-checks" + base + "origin", http.StatusBadRequest},
			{"/api/project/pr-checks" + base + "nope&sha=abc123", http.StatusNotFound},
		} {
			get(t, tc.target, tc.want, nil)
		}
	})
}
