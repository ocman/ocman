// Package local implements hostsvc.Host for the in-process machine. It
// is the ONLY package outside internal/server's own helpers that imports
// the git package; tmux launch, tmux listing, projects and whisper
// live in the server package and are injected as function dependencies
// to avoid an import cycle (server imports hostsvc/local, which would
// otherwise import server).
//
// No logic moves out of the git/server packages here — the local Host is
// a thin wrapper that owns the call site so handlers stop calling those
// helpers directly (AD-16, R-A).
package local

import (
	"context"
	"strings"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/git"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
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
	// TermWindows lists the in-app terminal windows for a directory.
	TermWindows func(dir string) ([]hostsvc.TermWindow, error)
	// TermCreateWindow creates a new terminal window for a directory and
	// returns its name.
	TermCreateWindow func(dir string) (string, error)
	// TermKillWindow kills the named terminal window for a directory.
	TermKillWindow func(dir, window string) error
	// TermAttach attaches a local PTY to the selected window and bridges
	// it to conn until either side closes.
	TermAttach func(ctx context.Context, req hostsvc.TermAttachRequest, conn hostsvc.TermConn) error
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

func (h *Host) GitInfo(ctx context.Context, dirs []string) (map[string]git.Info, error) {
	return git.LookupMany(ctx, dirs), nil
}

func (h *Host) GitDiff(ctx context.Context, dir string, opts hostsvc.GitDiffOptions) (*git.Diff, error) {
	return git.GetDiff(ctx, dir, git.DiffOptions{Force: opts.Force})
}

func (h *Host) GitBranches(ctx context.Context, dir string) ([]string, error) {
	return git.ListBranches(ctx, dir)
}

func (h *Host) GitCheckout(ctx context.Context, dir, branch string) error {
	return git.Checkout(ctx, dir, branch)
}

func (h *Host) ListWorktrees(ctx context.Context, dir string) ([]git.Worktree, error) {
	repoRoot, err := git.ResolveRepoRoot(ctx, dir)
	if err != nil {
		return nil, err
	}
	return git.ListWorktrees(ctx, repoRoot)
}

func (h *Host) WorktreeDefaultBaseRef(ctx context.Context, dir string) (string, error) {
	repoRoot, err := git.ResolveRepoRoot(ctx, dir)
	if err != nil {
		return "", err
	}
	return git.ResolveBaseRef(ctx, repoRoot), nil
}

func (h *Host) CreateWorktreeSession(ctx context.Context, req hostsvc.WorktreeSessionRequest) (*hostsvc.WorktreeSessionResult, error) {
	repoRoot, err := git.ResolveRepoRoot(ctx, req.ProjectDir)
	if err != nil {
		return nil, err
	}
	res, err := git.CreateWorktree(ctx, git.CreateWorktreeRequest{
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
	repoRoot, err := git.ResolveRepoRoot(ctx, req.Dir)
	if err != nil {
		return err
	}
	return git.RemoveWorktree(ctx, repoRoot, req.Path, req.Force)
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

func (h *Host) TermWindows(_ context.Context, dir string) ([]hostsvc.TermWindow, error) {
	if h.deps.TermWindows == nil {
		return nil, nil
	}
	return h.deps.TermWindows(dir)
}

func (h *Host) TermCreateWindow(_ context.Context, dir string) (string, error) {
	if h.deps.TermCreateWindow == nil {
		return "", nil
	}
	return h.deps.TermCreateWindow(dir)
}

func (h *Host) TermKillWindow(_ context.Context, dir, window string) error {
	if h.deps.TermKillWindow == nil {
		return nil
	}
	return h.deps.TermKillWindow(dir, window)
}

func (h *Host) TermAttach(ctx context.Context, req hostsvc.TermAttachRequest, conn hostsvc.TermConn) error {
	if h.deps.TermAttach == nil {
		return nil
	}
	return h.deps.TermAttach(ctx, req, conn)
}
