package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
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
	srv := &Server{registry: nil}
	// Use an empty registry rather than nil to avoid handler panics.
	// (The real Server constructor does this; we mimic it here so the
	// test can exercise handleCapabilities directly.)
	srv = &Server{}
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
// commit on `main`. Mirrors the helper in internal/worktree/ but lives
// here so handler tests don't depend on internal/worktree's package
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
	if !isTmuxAvailable() {
		t.Skip("tmux not available")
	}
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
	if !isTmuxAvailable() {
		t.Skip("tmux not available")
	}
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

// TestHandleWorktreeCreateAndLaunch_HappyPath exercises the full flow
// against a real git repo, using a stubbed tmux runner so we don't
// touch the test host's tmux server.
func TestHandleWorktreeCreateAndLaunch_HappyPath(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	// Replace the default tmux runner with a no-op stub for the
	// duration of the test. The launch helper already separates I/O
	// behind defaultTmuxRunner; we just need the calls to succeed
	// without spawning actual tmux processes.
	prev := defaultTmuxRunner
	t.Cleanup(func() { defaultTmuxRunner = prev })

	// We also need isTmuxAvailable to short-circuit true. The
	// handler checks via exec.LookPath("tmux"), so skip if missing
	// rather than try to fake that out (would require refactoring
	// isTmuxAvailable's lookup).
	if !isTmuxAvailable() {
		t.Skip("tmux not available")
	}

	repo := initWorktreeTestRepo(t)
	projectSessionName := tmuxSessionNameForPath(repo)

	defaultTmuxRunner = tmuxRunner{
		listSessions: func() ([]tmuxSession, error) {
			return []tmuxSession{{Name: projectSessionName, ResolvedPath: repo}}, nil
		},
		listWindows:    func(string) ([]tmuxWindow, error) { return nil, nil },
		newSession:     func(string, string, string) error { return nil },
		newWindow:      func(string, string, string) error { return nil },
		newNamedWindow: func(string, string, string, string) error { return nil },
	}
	srv := &Server{}

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
		WorktreePath     string `json:"worktreePath"`
		Branch           string `json:"branch"`
		Reused           bool   `json:"reused"`
		TmuxSession      string `json:"tmuxSession"`
		TmuxTarget       string `json:"tmuxTarget"`
		OpencodeLaunched bool   `json:"opencodeLaunched"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Branch != "feature/login" {
		t.Errorf("branch = %q; want feature/login", resp.Branch)
	}
	if resp.Reused {
		t.Errorf("reused = true on first call; want false")
	}
	if !resp.OpencodeLaunched {
		t.Errorf("opencodeLaunched = false; want true")
	}
	if !strings.Contains(resp.WorktreePath, "feature-login") {
		t.Errorf("worktreePath = %q; want it to contain the slug", resp.WorktreePath)
	}
	if resp.TmuxSession != projectSessionName {
		t.Errorf("tmuxSession = %q; want %q", resp.TmuxSession, projectSessionName)
	}
	if !strings.Contains(resp.TmuxTarget, ":wt-feature-login") {
		t.Errorf("tmuxTarget = %q; want :wt-feature-login suffix", resp.TmuxTarget)
	}

	// Re-run: expect Reused=true, OpencodeLaunched=false (idempotent).
	// Pre-populate the stub's window list with the named worktree window
	// so the launcher takes the idempotent short-circuit.
	defaultTmuxRunner.listSessions = func() ([]tmuxSession, error) {
		return []tmuxSession{{Name: projectSessionName, ResolvedPath: repo}}, nil
	}
	defaultTmuxRunner.listWindows = func(string) ([]tmuxWindow, error) {
		return []tmuxWindow{{Name: "wt-feature-login", Path: resp.WorktreePath}}, nil
	}

	rr2 := httptest.NewRecorder()
	srv.handleWorktreeCreateAndLaunch(rr2, httptest.NewRequest(http.MethodPost,
		"/api/worktree/create-and-launch",
		strings.NewReader(body)))
	if rr2.Code != http.StatusOK {
		t.Fatalf("re-run status = %d; body = %q", rr2.Code, rr2.Body.String())
	}
	var resp2 struct {
		Reused           bool `json:"reused"`
		OpencodeLaunched bool `json:"opencodeLaunched"`
	}
	if err := json.Unmarshal(rr2.Body.Bytes(), &resp2); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp2.Reused {
		t.Errorf("reused = false on second call; want true")
	}
	if resp2.OpencodeLaunched {
		t.Errorf("opencodeLaunched = true on idempotent re-run; want false")
	}
}
