package local

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ocman/ocman/internal/git"
	"github.com/ocman/ocman/internal/hostsvc"
)

func TestAutomaticWorktreeCleanupAndRestore(t *testing.T) {
	repo := initRepo(t)
	var err error
	repo, err = filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	created, err := git.CreateWorktree(t.Context(), git.CreateWorktreeRequest{RepoRoot: repo, Branch: "retained", NewBranch: true, BaseRef: "HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	entries, err := git.ListWorktrees(t.Context(), repo)
	if err != nil {
		t.Fatal(err)
	}
	var head string
	for _, entry := range entries {
		if entry.Path == created.Path {
			head = entry.Head
		}
	}
	req := hostsvc.CleanupWorktreeRequest{WorktreeCheckpoint: hostsvc.WorktreeCheckpoint{Project: repo, Path: created.Path, Branch: "retained", Head: head}}
	admitted, released := 0, 0
	h := New(Deps{AdmitWorktreeCleanup: func(ctx context.Context, got hostsvc.CleanupWorktreeRequest) (context.Context, func(), error) {
		if got.Path != req.Path {
			t.Fatal("wrong checkpoint")
		}
		admitted++
		return ctx, func() { released++ }, nil
	}})
	for _, tc := range []struct {
		name    string
		windows []hostsvc.TermWindow
		tmux    []hostsvc.TmuxSession
		err     error
	}{
		{name: "terminal", windows: []hostsvc.TermWindow{{Name: "shell"}}},
		{name: "tmux descendant", tmux: []hostsvc.TmuxSession{{Name: "shell", Path: created.Path + "/nested"}}},
		{name: "terminal probe failed", err: errors.New("unavailable")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h.deps.TermWindows = func(context.Context, string) ([]hostsvc.TermWindow, error) { return tc.windows, tc.err }
			h.deps.TmuxSessions = func(context.Context) ([]hostsvc.TmuxSession, error) { return tc.tmux, nil }
			if err := h.CleanupWorktree(t.Context(), req); err == nil {
				t.Fatal("unsafe cleanup accepted")
			}
			if admitted != released {
				t.Fatal("lease leaked")
			}
		})
	}
	h.deps.TermWindows = nil
	h.deps.TmuxSessions = func(context.Context) ([]hostsvc.TmuxSession, error) { return nil, errors.New("cannot inspect tmux") }
	if err := h.CleanupWorktree(t.Context(), req); err == nil {
		t.Fatal("ignored tmux error")
	}
	h.deps.TmuxSessions = nil
	if err := os.WriteFile(filepath.Join(created.Path, "notes"), []byte("valuable"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := h.CleanupWorktree(t.Context(), req); err == nil {
		t.Fatal("discarded untracked work")
	}
	if err := os.Remove(filepath.Join(created.Path, "notes")); err != nil {
		t.Fatal(err)
	}
	if err := h.CleanupWorktree(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	if admitted != released {
		t.Fatal("lease leaked on successful removal")
	}
	if _, err := os.Stat(created.Path); !os.IsNotExist(err) {
		t.Fatal("checkout not removed")
	}
	if err := h.RestoreWorktree(t.Context(), req.WorktreeCheckpoint); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(created.Path); err != nil {
		t.Fatal(err)
	}
}

func TestWorktreeMaintenanceFailsWithoutAdmission(t *testing.T) {
	h := New(Deps{})
	if err := h.CleanupWorktree(t.Context(), hostsvc.CleanupWorktreeRequest{}); err == nil {
		t.Fatal("cleanup without admission")
	}
	h.deps.AdmitWorktreeCleanup = func(ctx context.Context, _ hostsvc.CleanupWorktreeRequest) (context.Context, func(), error) {
		return ctx, nil, errors.New("active session")
	}
	if err := h.CleanupWorktree(t.Context(), hostsvc.CleanupWorktreeRequest{}); err == nil {
		t.Fatal("cleanup bypassed admission failure")
	}
	h.deps.WorktreeActivity = func(ctx context.Context, _ string) (context.Context, func(), error) {
		return ctx, nil, errors.New("cannot restore")
	}
	if _, err := h.TermCreateWindow(t.Context(), "/repo"); err == nil {
		t.Fatal("terminal create bypassed activity")
	}
	if err := h.TermKillWindow(t.Context(), "/repo", "window"); err == nil {
		t.Fatal("terminal close bypassed activity")
	}
	if err := h.TermAttach(t.Context(), hostsvc.TermAttachRequest{Dir: "/repo"}, nil); err == nil {
		t.Fatal("terminal attach bypassed activity")
	}
	if _, err := h.CreateWorktreeSession(t.Context(), hostsvc.WorktreeSessionRequest{ProjectDir: "/repo"}); err == nil {
		t.Fatal("worktree creation bypassed activity")
	}
}
