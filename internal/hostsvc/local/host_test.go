package local

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
)

func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
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
	var launchedDir, wtProject, wtPath string
	h := New(Deps{
		LaunchTmux: func(dir string) (string, error) {
			launchedDir = dir
			return "sess-name", nil
		},
		LaunchWorktreeTmux: func(projectDir, worktreeDir string) (string, bool, error) {
			wtProject, wtPath = projectDir, worktreeDir
			return "sess:win", true, nil
		},
		TmuxSessions: func() ([]hostsvc.TmuxSession, error) {
			return []hostsvc.TmuxSession{{Name: "s"}}, nil
		},
		Projects: func(context.Context) ([]db.ProjectStats, error) {
			return []db.ProjectStats{{Directory: "/p"}}, nil
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

	_ = wtProject
	_ = wtPath
}

func TestLocalHost_CreateWorktreeSession(t *testing.T) {
	repo := initRepo(t)
	var gotProject, gotWorktree string
	h := New(Deps{
		LaunchWorktreeTmux: func(projectDir, worktreeDir string) (string, bool, error) {
			gotProject, gotWorktree = projectDir, worktreeDir
			return "proj-sess:feature", true, nil
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
	if res.Branch != "feature" || res.WorktreePath == "" {
		t.Fatalf("unexpected result: %+v", res)
	}
	// Session name is split off the "session:window" target.
	if res.TmuxSession != "proj-sess" || res.TmuxTarget != "proj-sess:feature" {
		t.Errorf("tmux target split wrong: %+v", res)
	}
	if !res.OpencodeLaunched {
		t.Error("expected OpencodeLaunched true")
	}
	if gotProject == "" || gotWorktree == "" {
		t.Errorf("launcher not called with dirs: %q %q", gotProject, gotWorktree)
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
}
