// Package local implements hostsvc.Host for the in-process machine. It
// is the ONLY package outside internal/server's own helpers that imports
// gitinfo and worktree; tmux launch, tmux listing, projects and whisper
// live in the server package and are injected as function dependencies
// to avoid an import cycle (server imports hostsvc/local, which would
// otherwise import server).
//
// No logic moves out of gitinfo/worktree/server here — the local Host is
// a thin wrapper that owns the call site so handlers stop calling those
// helpers directly (AD-16, R-A).
package local

import (
	"context"
	"strings"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/gitinfo"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/worktree"
)

// Deps carries the host operations that live in the server package
// (tmux, projects, whisper availability). They are injected so this
// package does not import server.
type Deps struct {
	// LaunchTmux runs `opencode --port 0` in a tmux session for the
	// directory, returning the session name.
	LaunchTmux func(directory string) (string, error)
	// LaunchWorktreeTmux launches opencode in the shared "ocman-worktree"
	// tmux session under a named window rooted at the worktree path.
	// Returns (target, launched, err).
	LaunchWorktreeTmux func(projectDir, worktreeDir string) (string, bool, error)
	// TmuxSessions lists the host's tmux sessions.
	TmuxSessions func() ([]hostsvc.TmuxSession, error)
	// Projects returns the host's known projects.
	Projects func(ctx context.Context) ([]db.ProjectStats, error)
	// Caps reports which host operations are available right now
	// (tmux/git/opencode on PATH, whisper installed, etc.).
	Caps func() hostsvc.HostCaps
}

// Host is the local hostsvc.Host implementation.
type Host struct {
	deps Deps
}

// New returns a local Host wired with the given server-package deps.
func New(deps Deps) *Host { return &Host{deps: deps} }

// RemoteID is the routing/display sentinel for the local machine.
func (h *Host) RemoteID() string { return "local" }

func (h *Host) Capabilities() hostsvc.HostCaps {
	if h.deps.Caps != nil {
		return h.deps.Caps()
	}
	return hostsvc.HostCaps{}
}

func (h *Host) GitInfo(ctx context.Context, dirs []string) (map[string]gitinfo.Info, error) {
	return gitinfo.LookupMany(ctx, dirs), nil
}

func (h *Host) GitDiff(ctx context.Context, dir string, opts hostsvc.GitDiffOptions) (*gitinfo.Diff, error) {
	return gitinfo.GetDiff(ctx, dir, gitinfo.DiffOptions{Force: opts.Force})
}

func (h *Host) ListWorktrees(ctx context.Context, dir string) ([]worktree.Entry, error) {
	repoRoot, err := worktree.ResolveRepoRoot(ctx, dir)
	if err != nil {
		return nil, err
	}
	return worktree.List(ctx, repoRoot)
}

func (h *Host) WorktreeDefaultBaseRef(ctx context.Context, dir string) (string, error) {
	repoRoot, err := worktree.ResolveRepoRoot(ctx, dir)
	if err != nil {
		return "", err
	}
	return worktree.ResolveBaseRef(ctx, repoRoot), nil
}

func (h *Host) CreateWorktreeSession(ctx context.Context, req hostsvc.WorktreeSessionRequest) (*hostsvc.WorktreeSessionResult, error) {
	repoRoot, err := worktree.ResolveRepoRoot(ctx, req.ProjectDir)
	if err != nil {
		return nil, err
	}
	res, err := worktree.Create(ctx, worktree.CreateRequest{
		RepoRoot:  repoRoot,
		Branch:    req.Branch,
		NewBranch: req.NewBranch,
		BaseRef:   req.BaseRef,
	})
	if err != nil {
		return nil, err
	}
	target, launched, err := h.deps.LaunchWorktreeTmux(repoRoot, res.Path)
	if err != nil {
		return nil, err
	}
	session := target
	if i := strings.IndexByte(target, ':'); i >= 0 {
		session = target[:i]
	}
	return &hostsvc.WorktreeSessionResult{
		WorktreePath:     res.Path,
		Branch:           res.Branch,
		Reused:           res.Reused,
		BranchExisted:    res.BranchExisted,
		TmuxSession:      session,
		TmuxTarget:       target,
		OpencodeLaunched: launched,
	}, nil
}

func (h *Host) RemoveWorktree(ctx context.Context, req hostsvc.RemoveWorktreeRequest) error {
	repoRoot, err := worktree.ResolveRepoRoot(ctx, req.Dir)
	if err != nil {
		return err
	}
	return worktree.Remove(ctx, repoRoot, req.Path, req.Force)
}

func (h *Host) LaunchTmux(ctx context.Context, req hostsvc.LaunchTmuxRequest) (*hostsvc.LaunchTmuxResult, error) {
	name, err := h.deps.LaunchTmux(req.Directory)
	if err != nil {
		return nil, err
	}
	return &hostsvc.LaunchTmuxResult{Session: name}, nil
}

func (h *Host) TmuxSessions(ctx context.Context) ([]hostsvc.TmuxSession, error) {
	if h.deps.TmuxSessions == nil {
		return nil, nil
	}
	return h.deps.TmuxSessions()
}

func (h *Host) Projects(ctx context.Context) ([]db.ProjectStats, error) {
	if h.deps.Projects == nil {
		return nil, nil
	}
	return h.deps.Projects(ctx)
}
