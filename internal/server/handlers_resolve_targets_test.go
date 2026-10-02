package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/forge"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	hostlocal "github.com/NoUseFreak/ocman/internal/hostsvc/local"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/remote"
	"github.com/NoUseFreak/ocman/internal/testutil"
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

func TestHandleResolveTargets_RemoteOrigin(t *testing.T) {
	repo := initOriginRepo(t)
	srv := testServer(t)
	withManager(t, srv)
	srv.projects.mu.Lock()
	srv.projects.data = []db.ProjectStats{{Directory: repo}}
	srv.projects.loaded = true
	srv.projects.mu.Unlock()
	// The same absolute path on two hosts is not proof of project identity.
	host := &projectHandleRemoteHost{upstreams: &hostsvc.ProjectUpstreams{
		RepoRoot: repo,
		Remotes:  []forge.Remote{{Name: "origin", Host: "example.com", Repo: "org/repo"}},
	}}
	srv.router().RegisterRemote("rem1", host)
	for _, tc := range []struct {
		name, origin string
		want         int
	}{
		{"matching origin", "org/repo", 1},
		{"different origin same path", "other/repo", 0},
		{"no origin", "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			host.upstreams.Remotes[0].Repo = tc.origin
			rr := httptest.NewRecorder()
			srv.handleResolveTargets(rr, httptest.NewRequest(http.MethodPost, "/api/sessions/resolve-targets", bytes.NewBufferString(`{"dir":"`+repo+`","remoteId":"rem1"}`)))
			var resp struct {
				Candidates []remote.TargetCandidate `json:"candidates"`
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
				t.Fatal(err)
			}
			if rr.Code != http.StatusOK || len(resp.Candidates) != tc.want {
				t.Fatalf("status %d candidates %+v, want %d", rr.Code, resp.Candidates, tc.want)
			}
			if tc.want == 1 && resp.Candidates[0].Dir != repo {
				t.Fatalf("wrong target: %+v", resp.Candidates)
			}
		})
	}
}

func TestHandleResolveTargets_RejectsDisconnectedOwner(t *testing.T) {
	srv := testServer(t)
	rr := httptest.NewRecorder()
	srv.handleResolveTargets(rr, httptest.NewRequest(http.MethodPost, "/api/sessions/resolve-targets", bytes.NewBufferString(`{"dir":"/remote/repo","remoteId":"gone"}`)))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHandleResolveTargets_RemoteReadFailure(t *testing.T) {
	srv := testServer(t)
	withManager(t, srv)
	srv.router().RegisterRemote("rem1", &projectHandleRemoteHost{upstreamErr: errors.New("offline")})
	rr := httptest.NewRecorder()
	srv.handleResolveTargets(rr, httptest.NewRequest(http.MethodPost, "/api/sessions/resolve-targets", bytes.NewBufferString(`{"dir":"/remote/repo","remoteId":"rem1"}`)))
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHandleResolveTargets_RemoteRPCRoundTrip(t *testing.T) {
	localRepo, remoteRepo := initOriginRepo(t), initOriginRepo(t)
	srv := testServer(t)
	srv.projects.mu.Lock()
	srv.projects.data = []db.ProjectStats{{Directory: localRepo}}
	srv.projects.loaded = true
	srv.projects.mu.Unlock()
	registry := platforms.NewRegistry()
	registry.Register(&fakePlatform{id: "opencode"})
	service := remote.NewServer(registry, hostlocal.New(hostlocal.Deps{ProjectUpstreams: srv.hostProjectUpstreams}), "machine", "test")
	listener, err := remote.NewListener(remote.ListenConfig{Addr: "127.0.0.1:0", Token: "token", TrustedOverlay: true}, service)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = listener.Serve() }()
	t.Cleanup(listener.Stop)
	mgr := remote.NewManager(platforms.NewRegistry(), srv.router(), srv.stateDB, "opencode")
	srv.SetRemoteManager(mgr)
	t.Cleanup(mgr.Stop)
	if _, err := mgr.Add(t.Context(), "grpc://"+listener.Addr(), "token", "Remote"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	owner, connected := srv.router().LookupRemote("machine")
	for !connected {
		if time.Now().After(deadline) {
			t.Fatal("remote did not connect")
		}
		time.Sleep(5 * time.Millisecond)
		owner, connected = srv.router().LookupRemote("machine")
	}
	upstreams, err := owner.ProjectUpstreams(t.Context(), remoteRepo)
	if err != nil {
		t.Fatal(err)
	}
	if len(upstreams.Remotes) != 1 || upstreams.Remotes[0].URL != "" {
		t.Fatalf("expected redacted origin, got %+v", upstreams)
	}
	rr := httptest.NewRecorder()
	srv.handleResolveTargets(rr, httptest.NewRequest(http.MethodPost, "/api/sessions/resolve-targets", bytes.NewBufferString(`{"dir":"`+remoteRepo+`","remoteId":"machine"}`)))
	var resp struct {
		Candidates []remote.TargetCandidate `json:"candidates"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Candidates) != 1 || resp.Candidates[0].Dir != localRepo || resp.Candidates[0].RemoteID != "local" {
		t.Fatalf("status %d, candidates %+v; want local %s", rr.Code, resp.Candidates, localRepo)
	}
}

// initOriginRepo creates a git repo with an origin remote so
// localGitOrigin returns a non-empty URL.
func initOriginRepo(t *testing.T) string {
	t.Helper()
	testutil.RequireGit(t)
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(cleanGitEnvForTest(),
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
// manager-present path: localProjectMatch + localGitOrigin run, and
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

// TestHandleResolveTargets_ExactLocalDirWins: when the requested dir is
// itself a local project, it resolves to that dir without scanning the
// git origin of every other local project (which took seconds with a
// few hundred projects), even if an earlier project shares its identity.
func TestHandleResolveTargets_ExactLocalDirWins(t *testing.T) {
	first := filepath.Join(t.TempDir(), "repo")
	want := filepath.Join(t.TempDir(), "repo") // same basename identity
	srv := testServer(t)
	withManager(t, srv)
	srv.projects.mu.Lock()
	srv.projects.data = []db.ProjectStats{{Directory: first}, {Directory: want}}
	srv.projects.loaded = true
	srv.projects.mu.Unlock()

	req := httptest.NewRequest(http.MethodPost, "/api/sessions/resolve-targets", bytes.NewBufferString(`{"dir":"`+want+`"}`))
	rr := httptest.NewRecorder()
	srv.handleResolveTargets(rr, req)

	var resp struct {
		Candidates []remote.TargetCandidate `json:"candidates"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Candidates) != 1 || resp.Candidates[0].Dir != want {
		t.Fatalf("candidates = %+v, want local %s", resp.Candidates, want)
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
