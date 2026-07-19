package local

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/gitexec"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		// gitexec.CleanEnv strips GIT_DIR / GIT_INDEX_FILE etc. so this
		// helper can't touch the real repo when tests run under a git
		// hook inside a linked worktree.
		cmd.Env = append(gitexec.CleanEnv(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "f"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "f")
	run("commit", "-m", "init")
	return dir
}

func TestLocalHost_Identity(t *testing.T) {
	h := New(Deps{Caps: func() hostsvc.HostCaps { return hostsvc.HostCaps{GitDiff: true, Tmux: true} }})
	if h.RemoteID() != "local" {
		t.Errorf("RemoteID = %q", h.RemoteID())
	}
	if caps := h.Capabilities(); !caps.GitDiff || !caps.Tmux {
		t.Errorf("Capabilities = %+v", caps)
	}
	// Nil Caps yields the zero value rather than panicking.
	if (New(Deps{})).Capabilities() != (hostsvc.HostCaps{}) {
		t.Error("nil Caps should yield zero HostCaps")
	}
}

func TestLocalHost_ReadFileStaysInsideDirectory(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(filepath.Join(dir, "result.json"), []byte(`{"ok":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := New(Deps{})
	content, err := h.ReadFile(t.Context(), dir, "result.json")
	if err != nil || string(content) != `{"ok":true}` {
		t.Fatalf("ReadFile = %q, %v", content, err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "escape")); err != nil {
		t.Fatal(err)
	}
	if _, err := h.ReadFile(t.Context(), dir, "escape"); err == nil {
		t.Fatal("ReadFile followed a symlink outside the workflow directory")
	}
}

func TestLocalHost_GitMethods(t *testing.T) {
	repo := initRepo(t)
	h := New(Deps{})
	ctx := context.Background()

	infos, err := h.GitInfo(ctx, []string{repo})
	if err != nil {
		t.Fatalf("GitInfo: %v", err)
	}
	if !infos[repo].IsRepo() {
		t.Errorf("expected %s to be a repo: %+v", repo, infos[repo])
	}

	if _, err := h.GitDiff(ctx, repo, hostsvc.GitDiffOptions{Force: true}); err != nil {
		t.Errorf("GitDiff: %v", err)
	}

	entries, err := h.ListWorktrees(ctx, repo)
	if err != nil {
		t.Fatalf("ListWorktrees: %v", err)
	}
	if len(entries) == 0 || !entries[0].Main {
		t.Errorf("expected a main worktree, got %+v", entries)
	}

	ref, err := h.WorktreeDefaultBaseRef(ctx, repo)
	if err != nil || ref == "" {
		t.Errorf("WorktreeDefaultBaseRef = %q, %v", ref, err)
	}
}

func TestLocalHost_InjectedDeps(t *testing.T) {
	var launchedDir string
	h := New(Deps{
		LaunchTmux: func(dir string) (string, error) {
			launchedDir = dir
			return "sess-name", nil
		},
		TmuxSessions: func() ([]hostsvc.TmuxSession, error) {
			return []hostsvc.TmuxSession{{Name: "s"}}, nil
		},
		Projects: func(context.Context) ([]db.ProjectStats, error) {
			return []db.ProjectStats{{Directory: "/p"}}, nil
		},
		TermWindows: func(dir string) ([]hostsvc.TermWindow, error) {
			return []hostsvc.TermWindow{{Name: "w", Title: dir}}, nil
		},
		TermCreateWindow: func(dir string) (string, error) { return "win-" + dir, nil },
		TermKillWindow:   func(string, string) error { return nil },
		TermAttach: func(_ context.Context, req hostsvc.TermAttachRequest, _ hostsvc.TermConn) error {
			if req.Dir != "/d" {
				t.Errorf("TermAttach dir = %q", req.Dir)
			}
			return nil
		},
	})
	ctx := context.Background()

	res, err := h.LaunchTmux(ctx, hostsvc.LaunchTmuxRequest{Directory: "/d"})
	if err != nil || res.Session != "sess-name" || launchedDir != "/d" {
		t.Fatalf("LaunchTmux: res=%+v dir=%q err=%v", res, launchedDir, err)
	}

	sessions, err := h.TmuxSessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("TmuxSessions: %+v %v", sessions, err)
	}

	projects, err := h.Projects(ctx)
	if err != nil || len(projects) != 1 || projects[0].Directory != "/p" {
		t.Fatalf("Projects: %+v %v", projects, err)
	}

	wins, err := h.TermWindows(ctx, "/d")
	if err != nil || len(wins) != 1 || wins[0].Title != "/d" {
		t.Fatalf("TermWindows: %+v %v", wins, err)
	}
	name, err := h.TermCreateWindow(ctx, "/d")
	if err != nil || name != "win-/d" {
		t.Fatalf("TermCreateWindow: %q %v", name, err)
	}
	if err := h.TermKillWindow(ctx, "/d", "w"); err != nil {
		t.Fatalf("TermKillWindow: %v", err)
	}
	if err := h.TermAttach(ctx, hostsvc.TermAttachRequest{Dir: "/d"}, nil); err != nil {
		t.Fatalf("TermAttach: %v", err)
	}
}

// TestLocalHost_CreateWorktreeSession proves the #268 wiring: /wt creates
// the worktree, ensures the project's single opencode instance (reusing
// the already-running one — no launch, no per-worktree tmux window), and
// creates an in-app session rooted at the worktree on that instance's
// port. The result carries the created session ID.
func TestLocalHost_CreateWorktreeSession(t *testing.T) {
	repo := initRepo(t)
	var gotDirectory, gotPort string
	h := New(Deps{
		// The project instance launches on port 4242 and is healthy, so
		// CreateSession is created against it.
		Runtime: &fakeRuntime{endpoint: "http://127.0.0.1:4242"},
		CreateSession: func(_ context.Context, req platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
			gotDirectory, gotPort = req.Directory, req.Port
			return &platforms.CreateSessionResponse{ID: "ses_worktree"}, nil
		},
	})
	res, err := h.CreateWorktreeSession(context.Background(), hostsvc.WorktreeSessionRequest{
		ProjectDir: repo,
		Branch:     "feature",
		NewBranch:  true,
		BaseRef:    "main",
	})
	if err != nil {
		t.Fatalf("CreateWorktreeSession: %v", err)
	}
	if res.SessionID != "ses_worktree" {
		t.Errorf("SessionID = %q; want ses_worktree", res.SessionID)
	}
	if res.Branch != "feature" || res.WorktreePath == "" {
		t.Fatalf("unexpected result: %+v", res)
	}
	// The in-app session must be created against the worktree path on the
	// ensured instance's port.
	if gotDirectory != res.WorktreePath {
		t.Errorf("CreateSession directory = %q; want worktree path %q", gotDirectory, res.WorktreePath)
	}
	if gotPort != "4242" {
		t.Errorf("CreateSession port = %q; want ensured port 4242", gotPort)
	}
}

func TestLocalHost_RemoveWorktree(t *testing.T) {
	repo := initRepo(t)
	h := New(Deps{
		Runtime: &fakeRuntime{endpoint: "http://127.0.0.1:4242"},
		CreateSession: func(_ context.Context, _ platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
			return &platforms.CreateSessionResponse{ID: "ses_x"}, nil
		},
	})
	res, err := h.CreateWorktreeSession(context.Background(), hostsvc.WorktreeSessionRequest{
		ProjectDir: repo, Branch: "feature", NewBranch: true, BaseRef: "main",
	})
	if err != nil {
		t.Fatalf("CreateWorktreeSession: %v", err)
	}
	if err := h.RemoveWorktree(context.Background(), hostsvc.RemoveWorktreeRequest{
		Dir: repo, Path: res.WorktreePath,
	}); err != nil {
		t.Fatalf("RemoveWorktree: %v", err)
	}
}

// TestLocalHost_NilDepsAreSafe confirms the optional deps default safely.
func TestLocalHost_NilDepsAreSafe(t *testing.T) {
	h := New(Deps{})
	ctx := context.Background()
	if s, err := h.TmuxSessions(ctx); err != nil || s != nil {
		t.Errorf("TmuxSessions nil dep: %+v %v", s, err)
	}
	if p, err := h.Projects(ctx); err != nil || p != nil {
		t.Errorf("Projects nil dep: %+v %v", p, err)
	}
	if w, err := h.TermWindows(ctx, "/d"); err != nil || w != nil {
		t.Errorf("TermWindows nil dep: %+v %v", w, err)
	}
	if n, err := h.TermCreateWindow(ctx, "/d"); err != nil || n != "" {
		t.Errorf("TermCreateWindow nil dep: %q %v", n, err)
	}
	if err := h.TermKillWindow(ctx, "/d", "w"); err != nil {
		t.Errorf("TermKillWindow nil dep: %v", err)
	}
	if err := h.TermAttach(ctx, hostsvc.TermAttachRequest{}, nil); err != nil {
		t.Errorf("TermAttach nil dep: %v", err)
	}
}
