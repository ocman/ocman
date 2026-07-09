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

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	hostlocal "github.com/NoUseFreak/ocman/internal/hostsvc/local"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

// newEmptyRegistryForTest returns a registry with no adapters
// registered. The caps test uses it to assert worktreeSessions=false
// when OpenCode isn't on the host.
func newEmptyRegistryForTest() *platforms.Registry {
	return platforms.NewRegistry()
}

// TestCapabilities_WorktreeSessions verifies the top-level
// `worktreeSessions` boolean is present on the capabilities response.
// On a host without OpenCode registered (the empty-registry default
// in this test) it must be false — the feature is OpenCode-only in v1
// (AD-7).
func TestCapabilities_WorktreeSessions_FalseWithoutOpenCode(t *testing.T) {
	// Use an empty registry rather than nil to avoid handler panics.
	// (The real Server constructor does this; we mimic it here so the
	// test can exercise handleCapabilities directly.)
	srv := &Server{}
	srv.registry = newEmptyRegistryForTest()

	req := httptest.NewRequest(http.MethodGet, "/api/capabilities", nil)
	rr := httptest.NewRecorder()
	srv.handleCapabilities(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var resp struct {
		WorktreeSessions bool `json:"worktreeSessions"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.WorktreeSessions {
		t.Errorf("worktreeSessions = true with no OpenCode adapter; want false")
	}
}

// initWorktreeTestRepo seeds a fresh git repo in a temp dir with one
// commit on `main`. Mirrors the helper in internal/git/ but lives
// here so handler tests don't depend on internal/git's package
// internals.
//
// The repo is nested one level inside the temp dir (as "repo/") so
// that the `.worktrees` directory produced by PathFor lands inside the
// test's own isolated temp root and never collides with concurrent
// tests in other packages.
func initWorktreeTestRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	// Nest the repo under "repo/" so .worktrees/ stays inside this test's
	// unique temp root and never collides with other concurrent tests.
	root := t.TempDir()
	dir := filepath.Join(root, "repo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	// Resolve symlinks (macOS /var → /private/var) so later string
	// comparisons against `git rev-parse --show-toplevel` match.
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		// Strip git context variables so pre-commit hooks (or other
		// git tooling that sets GIT_DIR etc.) don't redirect these
		// commands into the wrong repository.
		cmd.Env = append(cleanGitEnvForTest(),
			"GIT_AUTHOR_NAME=test",
			"GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test",
			"GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	run("add", "README.md")
	run("commit", "-m", "initial")
	return dir
}

func TestHandleWorktreeList_HappyPath(t *testing.T) {
	repo := initWorktreeTestRepo(t)
	srv := &Server{}

	req := httptest.NewRequest(http.MethodGet, "/api/worktree/list?dir="+repo, nil)
	rr := httptest.NewRecorder()
	srv.handleWorktreeList(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %q", rr.Code, rr.Body.String())
	}
	var body struct {
		Worktrees []struct {
			Path   string `json:"path"`
			Branch string `json:"branch"`
			Main   bool   `json:"main"`
		} `json:"worktrees"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Worktrees) != 1 {
		t.Fatalf("got %d worktrees; want 1", len(body.Worktrees))
	}
	if body.Worktrees[0].Path != repo {
		t.Errorf("path = %q; want %q", body.Worktrees[0].Path, repo)
	}
	if body.Worktrees[0].Branch != "main" {
		t.Errorf("branch = %q; want main", body.Worktrees[0].Branch)
	}
	if !body.Worktrees[0].Main {
		t.Errorf("main = false; want true")
	}
}

func TestHandleWorktreeList_Errors(t *testing.T) {
	srv := &Server{}

	tests := []struct {
		name     string
		url      string
		wantCode int
	}{
		{"missing-dir", "/api/worktree/list", http.StatusBadRequest},
		{"relative-dir", "/api/worktree/list?dir=relative/path", http.StatusBadRequest},
		{"non-repo", "/api/worktree/list?dir=" + t.TempDir(), http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.url, nil)
			rr := httptest.NewRecorder()
			srv.handleWorktreeList(rr, req)
			if rr.Code != tt.wantCode {
				t.Errorf("status = %d; want %d (body: %q)", rr.Code, tt.wantCode, rr.Body.String())
			}
		})
	}
}

func TestHandleWorktreeDefaultBaseRef(t *testing.T) {
	repo := initWorktreeTestRepo(t)
	srv := &Server{}

	req := httptest.NewRequest(http.MethodGet, "/api/worktree/default-base-ref?dir="+repo, nil)
	rr := httptest.NewRecorder()
	srv.handleWorktreeDefaultBaseRef(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %q", rr.Code, rr.Body.String())
	}
	var body struct {
		BaseRef string `json:"baseRef"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Fresh test repo with no remotes -> resolver falls back to the
	// current branch ("main").
	if body.BaseRef != "main" {
		t.Errorf("baseRef = %q; want main", body.BaseRef)
	}
}

func TestHandleWorktreeCreateAndLaunch_BadInputs(t *testing.T) {
	// No tmux gate: input validation happens before any launch, and the
	// handler no longer requires tmux up-front (#268 runs sessions
	// in-app; tmux is only needed to launch a fresh project instance).
	srv := &Server{}

	tests := []struct {
		name     string
		body     string
		wantCode int
	}{
		{
			name:     "missing-projectDir",
			body:     `{"branch":"foo","newBranch":true,"baseRef":"main"}`,
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "relative-projectDir",
			body:     `{"projectDir":"relative","branch":"foo","newBranch":true,"baseRef":"main"}`,
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "missing-branch",
			body:     `{"projectDir":"/abs","newBranch":true,"baseRef":"main"}`,
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "missing-baseRef-when-newBranch",
			body:     `{"projectDir":"/abs","branch":"foo","newBranch":true}`,
			wantCode: http.StatusBadRequest,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost,
				"/api/worktree/create-and-launch",
				strings.NewReader(tt.body))
			rr := httptest.NewRecorder()
			srv.handleWorktreeCreateAndLaunch(rr, req)
			if rr.Code != tt.wantCode {
				t.Errorf("status = %d; want %d (body: %q)", rr.Code, tt.wantCode, rr.Body.String())
			}
		})
	}
}

func TestHandleWorktreeCreateAndLaunch_NonRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	srv := &Server{}

	notRepo := t.TempDir()
	body := `{"projectDir":"` + notRepo + `","branch":"foo","newBranch":true,"baseRef":"main"}`
	req := httptest.NewRequest(http.MethodPost,
		"/api/worktree/create-and-launch",
		strings.NewReader(body))
	rr := httptest.NewRecorder()
	srv.handleWorktreeCreateAndLaunch(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d; want 404 (body: %q)", rr.Code, rr.Body.String())
	}
}

func TestHandleWorktreeRemove_BadInputs(t *testing.T) {
	srv := &Server{}
	tests := []struct {
		name     string
		body     string
		wantCode int
	}{
		{"missing-projectDir", `{"path":"/abs/wt"}`, http.StatusBadRequest},
		{"missing-path", `{"projectDir":"/abs"}`, http.StatusBadRequest},
		{"relative-projectDir", `{"projectDir":"rel","path":"/abs/wt"}`, http.StatusBadRequest},
		{"relative-path", `{"projectDir":"/abs","path":"rel"}`, http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/worktree/remove", strings.NewReader(tt.body))
			rr := httptest.NewRecorder()
			srv.handleWorktreeRemove(rr, req)
			if rr.Code != tt.wantCode {
				t.Errorf("status = %d; want %d (body: %q)", rr.Code, tt.wantCode, rr.Body.String())
			}
		})
	}
}

func TestHandleWorktreeRemove_NonRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	srv := &Server{}
	notRepo := t.TempDir()
	body := `{"projectDir":"` + notRepo + `","path":"` + filepath.Join(notRepo, "wt") + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/worktree/remove", strings.NewReader(body))
	rr := httptest.NewRecorder()
	srv.handleWorktreeRemove(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d; want 404 (body: %q)", rr.Code, rr.Body.String())
	}
}

// TestHandleWorktreeRemove_MainWorktree confirms removing the primary
// checkout returns 409 (git refuses; we classify it).
func TestHandleWorktreeRemove_MainWorktree(t *testing.T) {
	repo := initWorktreeTestRepo(t)
	srv := &Server{}
	body := `{"projectDir":"` + repo + `","path":"` + repo + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/worktree/remove", strings.NewReader(body))
	rr := httptest.NewRecorder()
	srv.handleWorktreeRemove(rr, req)
	if rr.Code != http.StatusConflict {
		t.Errorf("status = %d; want 409 (body: %q)", rr.Code, rr.Body.String())
	}
}

// TestHandleWorktreeRemove_HappyPath creates a worktree directly, then
// removes it via the handler and confirms it's gone.
func TestHandleWorktreeRemove_HappyPath(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := initWorktreeTestRepo(t)
	wtPath := filepath.Join(t.TempDir(), "wt")
	add := exec.Command("git", "-C", repo, "worktree", "add", "-b", "feature/del", wtPath, "main")
	add.Env = cleanGitEnvForTest()
	if out, err := add.CombinedOutput(); err != nil {
		t.Fatalf("seed worktree: %v\n%s", err, out)
	}

	srv := &Server{}
	body := `{"projectDir":"` + repo + `","path":"` + wtPath + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/worktree/remove", strings.NewReader(body))
	rr := httptest.NewRecorder()
	srv.handleWorktreeRemove(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %q", rr.Code, rr.Body.String())
	}
	if _, statErr := os.Stat(wtPath); !os.IsNotExist(statErr) {
		t.Errorf("worktree dir still present: %v", statErr)
	}
}

// TestHandleWorktreeRemove_DirtyConflict confirms a dirty worktree
// returns 409 without force.
func TestHandleWorktreeRemove_DirtyConflict(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := initWorktreeTestRepo(t)
	wtPath := filepath.Join(t.TempDir(), "wt")
	add := exec.Command("git", "-C", repo, "worktree", "add", "-b", "feature/dirty", wtPath, "main")
	add.Env = cleanGitEnvForTest()
	if out, err := add.CombinedOutput(); err != nil {
		t.Fatalf("seed worktree: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(wtPath, "scratch.txt"), []byte("wip\n"), 0o644); err != nil {
		t.Fatalf("write scratch: %v", err)
	}

	srv := &Server{}
	body := `{"projectDir":"` + repo + `","path":"` + wtPath + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/worktree/remove", strings.NewReader(body))
	rr := httptest.NewRecorder()
	srv.handleWorktreeRemove(rr, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d; want 409 (body: %q)", rr.Code, rr.Body.String())
	}

	// Force must succeed.
	bodyForce := `{"projectDir":"` + repo + `","path":"` + wtPath + `","force":true}`
	rr2 := httptest.NewRecorder()
	srv.handleWorktreeRemove(rr2, httptest.NewRequest(http.MethodPost, "/api/worktree/remove", strings.NewReader(bodyForce)))
	if rr2.Code != http.StatusOK {
		t.Fatalf("force remove status = %d; body = %q", rr2.Code, rr2.Body.String())
	}
}

// TestHandleWorktreeCreateAndLaunch_HappyPath exercises the full flow
// against a real git repo, using a stubbed tmux runner so we don't
// touch the test host's tmux server.
func TestHandleWorktreeCreateAndLaunch_HappyPath(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repo := initWorktreeTestRepo(t)

	// #268: /wt now runs the session in-app. Inject a local host whose
	// DiscoverPort finds the (fake) already-running project instance and
	// whose CreateSession returns a canned session ID. Real git creates
	// the worktree, so the returned slug/path are genuine.
	var createDir, createPort string
	var createCalls int
	srv := &Server{}
	srv.hostRouter = hostsvc.NewRouter(hostlocal.New(hostlocal.Deps{
		DiscoverPort: func(string) string { return "5599" },
		CreateSession: func(_ context.Context, req platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
			createCalls++
			createDir, createPort = req.Directory, req.Port
			return &platforms.CreateSessionResponse{ID: "ses_wt_login"}, nil
		},
	}))

	body := `{"projectDir":"` + repo + `","branch":"feature/login","newBranch":true,"baseRef":"main"}`
	req := httptest.NewRequest(http.MethodPost,
		"/api/worktree/create-and-launch",
		strings.NewReader(body))
	rr := httptest.NewRecorder()
	srv.handleWorktreeCreateAndLaunch(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %q", rr.Code, rr.Body.String())
	}
	var resp struct {
		SessionID    string `json:"sessionId"`
		WorktreePath string `json:"worktreePath"`
		Branch       string `json:"branch"`
		Reused       bool   `json:"reused"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.SessionID != "ses_wt_login" {
		t.Errorf("sessionId = %q; want ses_wt_login", resp.SessionID)
	}
	if resp.Branch != "feature/login" {
		t.Errorf("branch = %q; want feature/login", resp.Branch)
	}
	if resp.Reused {
		t.Errorf("reused = true on first call; want false")
	}
	if !strings.Contains(resp.WorktreePath, "feature-login") {
		t.Errorf("worktreePath = %q; want it to contain the slug", resp.WorktreePath)
	}
	// The in-app session must be created rooted at the worktree on the
	// ensured project port.
	if createDir != resp.WorktreePath {
		t.Errorf("CreateSession dir = %q; want worktree %q", createDir, resp.WorktreePath)
	}
	if createPort != "5599" {
		t.Errorf("CreateSession port = %q; want ensured 5599", createPort)
	}

	// Re-run: expect Reused=true (idempotent worktree), a second in-app
	// session created on the same instance.
	rr2 := httptest.NewRecorder()
	srv.handleWorktreeCreateAndLaunch(rr2, httptest.NewRequest(http.MethodPost,
		"/api/worktree/create-and-launch",
		strings.NewReader(body)))
	if rr2.Code != http.StatusOK {
		t.Fatalf("re-run status = %d; body = %q", rr2.Code, rr2.Body.String())
	}
	var resp2 struct {
		Reused bool `json:"reused"`
	}
	if err := json.Unmarshal(rr2.Body.Bytes(), &resp2); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp2.Reused {
		t.Errorf("reused = false on second call; want true")
	}
	if createCalls != 2 {
		t.Errorf("CreateSession calls = %d; want 2 (one per /wt)", createCalls)
	}
}

// worktreeInheritTestServer wires a server with a real git repo, a fake
// host that returns a canned worktree session, and a fake "opencode"
// platform capturing SetPermissionRules — the seam issue #101 uses to
// seed the new worktree session with the parent's approvals.
func worktreeInheritTestServer(t *testing.T, capture *[]platforms.SetPermissionRulesRequest) (*Server, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := initWorktreeTestRepo(t)

	srv, reg := newSessionsTestServer(t)
	fake := &fakePlatform{
		id:       "opencode",
		sessions: []db.Session{mkSession("opencode", "ses_wt_inh", "t", 1000)},
		setPermissionRulesFn: func(req platforms.SetPermissionRulesRequest) error {
			*capture = append(*capture, req)
			return nil
		},
	}
	reg.Register(fake)

	srv.hostRouter = hostsvc.NewRouter(hostlocal.New(hostlocal.Deps{
		DiscoverPort: func(string) string { return "5599" },
		CreateSession: func(_ context.Context, _ platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
			return &platforms.CreateSessionResponse{ID: "ses_wt_inh"}, nil
		},
	}))
	return srv, repo
}

func TestHandleWorktreeCreateAndLaunch_InheritsParentPermissions(t *testing.T) {
	var captured []platforms.SetPermissionRulesRequest
	srv, repo := worktreeInheritTestServer(t, &captured)

	if err := srv.stateDB.RecordApprovedPermission("opencode", "parent-wt", state.ApprovedPermission{
		PermissionID:   "perm-1",
		PermissionText: "bash",
		Patterns:       []string{"git *"},
		ApprovedAt:     1000,
	}); err != nil {
		t.Fatalf("RecordApprovedPermission: %v", err)
	}

	body := `{"projectDir":"` + repo + `","branch":"feature/inh","newBranch":true,"baseRef":"main","parentSessionId":"parent-wt"}`
	rr := httptest.NewRecorder()
	srv.handleWorktreeCreateAndLaunch(rr, httptest.NewRequest(http.MethodPost, "/api/worktree/create-and-launch", strings.NewReader(body)))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %q", rr.Code, rr.Body.String())
	}

	var resp struct {
		PermissionsInherited      bool   `json:"permissionsInherited"`
		PermissionsInheritedCount int    `json:"permissionsInheritedCount"`
		PermissionsInheritError   string `json:"permissionsInheritError"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.PermissionsInherited || resp.PermissionsInheritedCount != 1 || resp.PermissionsInheritError != "" {
		t.Fatalf("result = %+v, want inherited/1/no-error", resp)
	}
	if len(captured) != 1 {
		t.Fatalf("SetPermissionRules calls = %d, want 1", len(captured))
	}
	if captured[0].SessionID != "ses_wt_inh" || len(captured[0].Rules) != 1 ||
		captured[0].Rules[0].Permission != "bash" || captured[0].Rules[0].Pattern != "git *" {
		t.Fatalf("unexpected applied rules: %+v", captured[0])
	}
}

func TestHandleWorktreeCreateAndLaunch_NoParentNoInherit(t *testing.T) {
	var captured []platforms.SetPermissionRulesRequest
	srv, repo := worktreeInheritTestServer(t, &captured)

	body := `{"projectDir":"` + repo + `","branch":"feature/noparent","newBranch":true,"baseRef":"main"}`
	rr := httptest.NewRecorder()
	srv.handleWorktreeCreateAndLaunch(rr, httptest.NewRequest(http.MethodPost, "/api/worktree/create-and-launch", strings.NewReader(body)))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %q", rr.Code, rr.Body.String())
	}
	var resp struct {
		PermissionsInherited bool `json:"permissionsInherited"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.PermissionsInherited {
		t.Errorf("permissionsInherited = true with no parentSessionId, want false")
	}
	if len(captured) != 0 {
		t.Errorf("SetPermissionRules calls = %d, want 0", len(captured))
	}
}

func TestHandleWorktreeCreateAndLaunch_InheritDisabled(t *testing.T) {
	var captured []platforms.SetPermissionRulesRequest
	srv, repo := worktreeInheritTestServer(t, &captured)

	if err := srv.stateDB.SetWorktreeInheritPermissions(false); err != nil {
		t.Fatalf("SetWorktreeInheritPermissions: %v", err)
	}
	if err := srv.stateDB.RecordApprovedPermission("opencode", "parent-wt", state.ApprovedPermission{
		PermissionID: "perm-1", PermissionText: "bash", Patterns: []string{"git *"}, ApprovedAt: 1000,
	}); err != nil {
		t.Fatalf("RecordApprovedPermission: %v", err)
	}

	body := `{"projectDir":"` + repo + `","branch":"feature/off","newBranch":true,"baseRef":"main","parentSessionId":"parent-wt"}`
	rr := httptest.NewRecorder()
	srv.handleWorktreeCreateAndLaunch(rr, httptest.NewRequest(http.MethodPost, "/api/worktree/create-and-launch", strings.NewReader(body)))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %q", rr.Code, rr.Body.String())
	}
	if len(captured) != 0 {
		t.Errorf("SetPermissionRules calls = %d, want 0 when setting off", len(captured))
	}
}
