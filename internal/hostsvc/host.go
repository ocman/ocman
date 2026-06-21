// Package hostsvc defines the directory/host-scoped adapter seam — the
// directory analogue of internal/platforms. Where Platform owns
// session-scoped operations resolved by Registry.PlatformForSession,
// Host owns directory-scoped operations (git, worktree, tmux, projects,
// whisper) resolved by Router.ForDir / Router.ForRemote.
//
// The rule that keeps remote support from becoming a tax on every
// future feature: every operation that touches a machine's filesystem,
// processes, git working tree, or agent runtime is expressed as a method
// on an owner-resolved Host, never as a direct call to a package-level
// local helper from an HTTP handler. The local Host (internal/hostsvc/
// local) is the only place that imports gitinfo/worktree/tmux/whisper;
// a gRPC-backed Host (internal/remote) proxies the same methods to the
// owning remote, which executes its own local Host.
//
// See spec/multi-remote-support/architecture.md AD-16.
package hostsvc

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/gitinfo"
	"github.com/NoUseFreak/ocman/internal/worktree"
)

// HostCaps reports which directory-scoped capabilities a host supports.
// Surfaced via /api/capabilities so the frontend can gate host-touching
// UI on flags rather than on remote identity (R-B). v1 local and remote
// OpenCode hosts report all true.
type HostCaps struct {
	GitDiff   bool `json:"gitDiff"`
	Worktrees bool `json:"worktrees"`
	Tmux      bool `json:"tmux"`
	Projects  bool `json:"projects"`
	Whisper   bool `json:"whisper"`
}

// GitDiffOptions controls a GitDiff call.
type GitDiffOptions struct {
	Force bool
}

// WorktreeSessionRequest captures a create-worktree-and-launch action.
type WorktreeSessionRequest struct {
	ProjectDir string
	Branch     string
	NewBranch  bool
	BaseRef    string
}

// WorktreeSessionResult is the outcome of CreateWorktreeSession: the
// resulting worktree plus the tmux target it was launched into.
type WorktreeSessionResult struct {
	WorktreePath     string `json:"worktreePath"`
	Branch           string `json:"branch"`
	Reused           bool   `json:"reused"`
	BranchExisted    bool   `json:"branchExisted"`
	TmuxSession      string `json:"tmuxSession"`
	TmuxTarget       string `json:"tmuxTarget"`
	OpencodeLaunched bool   `json:"opencodeLaunched"`
}

// LaunchTmuxRequest launches `opencode --port 0` in a tmux session
// rooted at Directory.
type LaunchTmuxRequest struct {
	Directory string
}

// LaunchTmuxResult reports the tmux session that was used/created.
type LaunchTmuxResult struct {
	Session string `json:"session"`
}

// TmuxSession is one tmux session reported by TmuxSessions.
type TmuxSession struct {
	Name     string `json:"name"`
	Path     string `json:"path,omitempty"`
	Attached bool   `json:"attached,omitempty"`
}

// Host is the contract for directory-scoped host operations. The owner
// of a path (local machine or a remote) implements it; handlers resolve
// the owner via Router and delegate.
//
// All methods take an absolute directory on the owning host. Errors are
// returned verbatim; handlers translate sentinel errors (gitinfo.ErrNotRepo,
// worktree.ErrNotARepo, ...) into HTTP status codes as before.
type Host interface {
	// RemoteID returns "local" for the in-process host, else the
	// remote's instance ID. Display/routing only.
	RemoteID() string

	// Capabilities reports which host operations are available.
	Capabilities() HostCaps

	// GitInfo returns per-directory git status for each dir.
	GitInfo(ctx context.Context, dirs []string) (map[string]gitinfo.Info, error)

	// GitDiff returns the working-tree diff for dir.
	GitDiff(ctx context.Context, dir string, opts GitDiffOptions) (*gitinfo.Diff, error)

	// ListWorktrees returns parsed `git worktree list` for the repo
	// containing dir.
	ListWorktrees(ctx context.Context, dir string) ([]worktree.Entry, error)

	// WorktreeDefaultBaseRef returns the resolver's best-guess base ref
	// for new worktrees in the repo containing dir.
	WorktreeDefaultBaseRef(ctx context.Context, dir string) (string, error)

	// CreateWorktreeSession creates (or reuses) a worktree and launches
	// opencode in tmux rooted at it. Runs on the owning host (R-C).
	CreateWorktreeSession(ctx context.Context, req WorktreeSessionRequest) (*WorktreeSessionResult, error)

	// LaunchTmux launches opencode in a tmux session for a directory.
	LaunchTmux(ctx context.Context, req LaunchTmuxRequest) (*LaunchTmuxResult, error)

	// TmuxSessions lists the host's tmux sessions.
	TmuxSessions(ctx context.Context) ([]TmuxSession, error)

	// Projects returns the host's known projects (its projects index).
	Projects(ctx context.Context) ([]db.ProjectStats, error)
}
