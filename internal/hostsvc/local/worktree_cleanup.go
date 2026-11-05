package local

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/ocman/ocman/internal/git"
	"github.com/ocman/ocman/internal/hostsvc"
)

func (h *Host) beginWorktreeActivity(ctx context.Context, dir string) (context.Context, func(), error) {
	if h.deps.WorktreeActivity == nil {
		return ctx, func() {}, nil
	}
	return h.deps.WorktreeActivity(ctx, dir)
}

func (h *Host) CleanupWorktree(ctx context.Context, req hostsvc.CleanupWorktreeRequest) error {
	if h.deps.AdmitWorktreeCleanup == nil {
		return fmt.Errorf("automatic cleanup is unavailable")
	}
	ctx, release, err := h.deps.AdmitWorktreeCleanup(ctx, req)
	if err != nil {
		return err
	}
	defer release()
	if h.deps.CheckWorktreeTerminals != nil {
		if err := h.deps.CheckWorktreeTerminals(ctx, req.Path); err != nil {
			return err
		}
	}
	// Check both managed terminals and all tmux sessions on the owner. Any
	// inspection error keeps the checkout, including an unavailable tmux server.
	windows, err := h.TermWindows(ctx, req.Path)
	if err != nil {
		return err
	}
	if len(windows) != 0 {
		return fmt.Errorf("worktree has terminal windows")
	}
	sessions, err := h.TmuxSessions(ctx)
	if err != nil {
		return err
	}
	for _, session := range sessions {
		path := filepath.Clean(session.Path)
		if path == req.Path || strings.HasPrefix(path, req.Path+string(filepath.Separator)) {
			return fmt.Errorf("worktree has a tmux session")
		}
	}
	if err := git.CheckCleanupWorktree(ctx, req.Project, req.Path, req.Branch, req.Head); err != nil {
		return err
	}
	return git.RemoveWorktree(ctx, req.Project, req.Path, false)
}

func (h *Host) RestoreWorktree(ctx context.Context, checkpoint hostsvc.WorktreeCheckpoint) error {
	if h.deps.WorktreeLease != nil {
		leaseCtx, release := h.deps.WorktreeLease(ctx)
		defer release()
		ctx = leaseCtx
	}
	return git.RestoreCleanupWorktree(ctx, checkpoint.Project, checkpoint.Path, checkpoint.Branch, checkpoint.Head)
}
