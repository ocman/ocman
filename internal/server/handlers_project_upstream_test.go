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
	srv.hostRouter = hostsvc.NewRouter(&ownerSpy{})
	srv.hostRouter.RegisterRemote("rem1", host)

	rr := httptest.NewRecorder()
	serveProjectForge(srv, rr, httptest.NewRequest(http.MethodGet, "/api/project/upstreams?dir=/remote/only/repo&remoteId=rem1", nil))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"repo":"alice/myproj"`) {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
}

func TestHandleProjectUpstreams_RejectsUnknownExplicitOwner(t *testing.T) {
	srv := testServer(t)
	rr := httptest.NewRecorder()
	serveProjectForge(srv, rr, httptest.NewRequest(http.MethodGet, "/api/project/upstreams?dir=/remote/only/repo&remoteId=gone", nil))
	if rr.Code != http.StatusServiceUnavailable {
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
	srv.hostRouter = hostsvc.NewRouter(&ownerSpy{})
	srv.hostRouter.RegisterRemote("rem1", host)

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
	srv.hostRouter = hostsvc.NewRouter(&ownerSpy{})
	srv.hostRouter.RegisterRemote("rem1", host)
	rr := httptest.NewRecorder()
	serveProjectForge(srv, rr, httptest.NewRequest(http.MethodGet, "/api/project/upstreams?dir=/remote/nope&remoteId=rem1", nil))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
}

func TestHandleProjectUpstreams_RejectsInvalidOwnerResult(t *testing.T) {
	srv := testServer(t)
	host := &projectHandleRemoteHost{upstreams: githubProjectUpstreams("relative")}
	srv.hostRouter = hostsvc.NewRouter(&ownerSpy{})
	srv.hostRouter.RegisterRemote("rem1", host)
	rr := httptest.NewRecorder()
	serveProjectForge(srv, rr, httptest.NewRequest(http.MethodGet, "/api/project/upstreams?dir=/repo&remoteId=rem1", nil))
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
}

func TestHandleProjectUpstreams_RejectsNonAbsoluteDir(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/project/upstreams?dir=relative/path", nil)
	rr := httptest.NewRecorder()
	serveProjectForge(srv, rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

func TestProjectUpstreamEndpointsRequireExplicitOwnerAndAbsoluteDir(t *testing.T) {
	srv := testServer(t)
	tests := []string{
		"/api/project/upstreams?dir=/repo",
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
			switch req.URL.Path {
			case "/api/project/upstreams":
				serveProjectForge(srv, rr, req)
			case "/api/project/prs":
				serveProjectForge(srv, rr, req)
			case "/api/project/issues":
				serveProjectForge(srv, rr, req)
			case "/api/project/forge-user":
				serveProjectForge(srv, rr, req)
			case "/api/project/pr-checks":
				serveProjectForge(srv, rr, req)
			}
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
	client := github.NewForTest(srv.URL, "test-token", srv.Client())
	return srv, client
}

func TestHandleProjectPRs_ReturnsList(t *testing.T) {
	srv := testServer(t)
	dir := initGitHubRepo(t)
	gh, ghc := fakeGitHubServer(t)
	defer gh.Close()
	srv.integrations.GitHub = ghc

	req := httptest.NewRequest(http.MethodGet,
		"/api/project/prs?dir="+dir+"&remoteId=local&remote=origin&state=open", nil)
	rr := httptest.NewRecorder()
	serveProjectForge(srv, rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	var resp struct {
		PRs []forge.PR `json:"prs"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, rr.Body.String())
	}
	if len(resp.PRs) != 1 || resp.PRs[0].Number != 42 {
		t.Errorf("unexpected prs: %+v", resp.PRs)
	}
}

func TestHandleProjectPRs_MineFiltersToAssigneeOrReviewer(t *testing.T) {
	srv := testServer(t)
	dir := initGitHubRepo(t)
	gh, ghc := fakeGitHubServer(t)
	defer gh.Close()
	srv.integrations.GitHub = ghc

	// "alice" is the author + assignee → matches.
	req := httptest.NewRequest(http.MethodGet,
		"/api/project/prs?dir="+dir+"&remoteId=local&remote=origin&mine=alice", nil)
	rr := httptest.NewRecorder()
	serveProjectForge(srv, rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var resp struct {
		PRs []forge.PR `json:"prs"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if len(resp.PRs) != 1 {
		t.Errorf("alice should match (author/assignee), got %d", len(resp.PRs))
	}

	// "bob" is a requested reviewer → also matches.
	req = httptest.NewRequest(http.MethodGet,
		"/api/project/prs?dir="+dir+"&remoteId=local&remote=origin&mine=bob", nil)
	rr = httptest.NewRecorder()
	serveProjectForge(srv, rr, req)
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if len(resp.PRs) != 1 {
		t.Errorf("bob should match (reviewer), got %d", len(resp.PRs))
	}

	// "stranger" matches nothing.
	req = httptest.NewRequest(http.MethodGet,
		"/api/project/prs?dir="+dir+"&remoteId=local&remote=origin&mine=stranger", nil)
	rr = httptest.NewRecorder()
	serveProjectForge(srv, rr, req)
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if len(resp.PRs) != 0 {
		t.Errorf("stranger should match nothing, got %d", len(resp.PRs))
	}
}

func TestHandleProjectIssues_ReturnsList(t *testing.T) {
	srv := testServer(t)
	dir := initGitHubRepo(t)
	gh, ghc := fakeGitHubServer(t)
	defer gh.Close()
	srv.integrations.GitHub = ghc

	req := httptest.NewRequest(http.MethodGet,
		"/api/project/issues?dir="+dir+"&remoteId=local&remote=origin", nil)
	rr := httptest.NewRecorder()
	serveProjectForge(srv, rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Issues []forge.Issue `json:"issues"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if len(resp.Issues) != 1 || resp.Issues[0].Number != 7 {
		t.Errorf("unexpected issues: %+v", resp.Issues)
	}
}

func TestHandleProjectIssues_MineFiltersAssignees(t *testing.T) {
	srv := testServer(t)
	dir := initGitHubRepo(t)
	gh, ghc := fakeGitHubServer(t)
	defer gh.Close()
	srv.integrations.GitHub = ghc

	for login, want := range map[string]int{"alice": 1, "stranger": 0} {
		rr := httptest.NewRecorder()
		serveProjectForge(srv, rr, httptest.NewRequest(http.MethodGet,
			"/api/project/issues?dir="+dir+"&remoteId=local&remote=origin&mine="+login, nil))
		var resp struct {
			Issues []forge.Issue `json:"issues"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil || len(resp.Issues) != want {
			t.Fatalf("login %s: status = %d, issues = %d, err = %v", login, rr.Code, len(resp.Issues), err)
		}
	}
}

func TestProjectForgeMetadataErrorsAreMapped(t *testing.T) {
	dir := initGitHubRepo(t)
	tests := []struct {
		name   string
		path   string
		status int
	}{
		{"prs", "/api/project/prs?dir=" + dir + "&remoteId=local&remote=origin", http.StatusBadGateway},
		{"issues", "/api/project/issues?dir=" + dir + "&remoteId=local&remote=origin", http.StatusBadGateway},
		{"forge user", "/api/project/forge-user?dir=" + dir + "&remoteId=local&remote=origin", http.StatusBadGateway},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := testServer(t)
			gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "upstream failed", http.StatusInternalServerError)
			}))
			defer gh.Close()
			srv.integrations.GitHub = github.NewForTest(gh.URL, "test-token", gh.Client())
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			switch tt.name {
			case "prs":
				serveProjectForge(srv, rr, req)
			case "issues":
				serveProjectForge(srv, rr, req)
			default:
				serveProjectForge(srv, rr, req)
			}
			if rr.Code != tt.status {
				t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
			}
		})
	}
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

func TestHandleProjectForgeUser_ReturnsLogin(t *testing.T) {
	srv := testServer(t)
	dir := initGitHubRepo(t)
	gh, ghc := fakeGitHubServer(t)
	defer gh.Close()
	srv.integrations.GitHub = ghc

	req := httptest.NewRequest(http.MethodGet,
		"/api/project/forge-user?dir="+dir+"&remoteId=local&remote=origin", nil)
	rr := httptest.NewRecorder()
	serveProjectForge(srv, rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	var u forge.CurrentUser
	_ = json.Unmarshal(rr.Body.Bytes(), &u)
	if u.Login != "alice" || u.Host != "github.com" {
		t.Errorf("got %+v", u)
	}
}

func TestHandleProjectPRs_404ForUnknownRemote(t *testing.T) {
	srv := testServer(t)
	dir := initGitHubRepo(t)
	req := httptest.NewRequest(http.MethodGet,
		"/api/project/prs?dir="+dir+"&remoteId=local&remote=nonexistent", nil)
	rr := httptest.NewRecorder()
	serveProjectForge(srv, rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandleProjectPRs_RejectsInvalidState(t *testing.T) {
	srv := testServer(t)
	dir := initGitHubRepo(t)
	req := httptest.NewRequest(http.MethodGet,
		"/api/project/prs?dir="+dir+"&remoteId=local&remote=origin&state=invalid", nil)
	rr := httptest.NewRecorder()
	serveProjectForge(srv, rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandleProjectPRChecks_ReturnsRolledUpStatus(t *testing.T) {
	srv := testServer(t)
	dir := initGitHubRepo(t)
	gh, ghc := fakeGitHubServer(t)
	defer gh.Close()
	srv.integrations.GitHub = ghc

	req := httptest.NewRequest(http.MethodGet,
		"/api/project/pr-checks?dir="+dir+"&remoteId=local&remote=origin&sha=abc123", nil)
	rr := httptest.NewRecorder()
	serveProjectForge(srv, rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	var resp struct {
		State  forge.CIState `json:"state"`
		Checks []forge.Check `json:"checks"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, rr.Body.String())
	}
	if resp.State != forge.CIStateSuccess {
		t.Errorf("state: got %q want success", resp.State)
	}
	if len(resp.Checks) != 2 {
		t.Errorf("expected 2 checks (status + check-run), got %d", len(resp.Checks))
	}
}

func TestHandleProjectPRChecks_RequiresParams(t *testing.T) {
	srv := testServer(t)
	dir := initGitHubRepo(t)
	// Missing sha.
	req := httptest.NewRequest(http.MethodGet,
		"/api/project/pr-checks?dir="+dir+"&remoteId=local&remote=origin", nil)
	rr := httptest.NewRecorder()
	serveProjectForge(srv, rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing sha, got %d", rr.Code)
	}
}

func TestHandleProjectPRChecks_404ForUnknownRemote(t *testing.T) {
	srv := testServer(t)
	dir := initGitHubRepo(t)
	req := httptest.NewRequest(http.MethodGet,
		"/api/project/pr-checks?dir="+dir+"&remoteId=local&remote=nope&sha=abc123", nil)
	rr := httptest.NewRecorder()
	serveProjectForge(srv, rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandleProjectPRChecks_502WhenForgeErrors(t *testing.T) {
	srv := testServer(t)
	dir := initGitHubRepo(t)

	// A forge whose checks endpoints return 500 so Checks() errors,
	// which the handler must translate into a 502 upstream_status.
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer gh.Close()
	srv.integrations.GitHub = github.NewForTest(gh.URL, "test-token", gh.Client())

	req := httptest.NewRequest(http.MethodGet,
		"/api/project/pr-checks?dir="+dir+"&remoteId=local&remote=origin&sha=abc123", nil)
	rr := httptest.NewRecorder()
	serveProjectForge(srv, rr, req)

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d body=%s", rr.Code, rr.Body.String())
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
	srv.integrations.GitHub = github.NewForTest(gh.URL, "test-token", gh.Client())

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
