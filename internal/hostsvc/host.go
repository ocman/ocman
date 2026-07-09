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
// local) is the only place that imports the git package/tmux/whisper;
// a gRPC-backed Host (internal/remote) proxies the same methods to the
// owning remote, which executes its own local Host.
//
// See spec/multi-remote-support/architecture.md AD-16.
package hostsvc

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/git"
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

// TermWindow is one in-app terminal window with a display title. Mirrors
// the JSON shape the /api/term/windows handler returns.
type TermWindow struct {
	Name  string `json:"name"`
	Title string `json:"title"`
}

// TermAttachRequest selects the terminal window to attach a PTY to.
type TermAttachRequest struct {
	Dir      string
	Window   string
	Readonly bool
}

// TermSize is a terminal resize request in character cells.
type TermSize struct {
	Cols uint16
	Rows uint16
}

// TermFrame is a single viewer->PTY frame. Exactly one of Data / Resize
// is set: Data carries raw keystrokes, Resize a window resize.
type TermFrame struct {
	Data   []byte
	Resize *TermSize
}

// TermConn is the browser side of a terminal attach: the Host reads the
// viewer's frames (keystrokes + resizes) from it and writes PTY output
// back. It is the transport-agnostic seam so the local Host (direct PTY)
// and the remote Host (gRPC-tunnelled PTY) share one Attach signature.
//
// Recv returns the next viewer frame, blocking until one is available or
// the connection closes (io.EOF). Write delivers PTY output to the
// viewer. Close tears the connection down from either side.
type TermConn interface {
	Recv() (TermFrame, error)
	Write(p []byte) error
	Close() error
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
// created in-app session (SessionID, the primary output) plus the
// worktree it is rooted at. The Tmux* / OpencodeLaunched fields are
// legacy remnants of the per-worktree tmux launcher; the /wt path no
// longer populates them (#268) and #269 removes them.
type WorktreeSessionResult struct {
	// SessionID is the OpenCode session created in-app on the project's
	// single opencode instance, rooted at WorktreePath.
	SessionID        string `json:"sessionId"`
	WorktreePath     string `json:"worktreePath"`
	Branch           string `json:"branch"`
	Reused           bool   `json:"reused"`
	BranchExisted    bool   `json:"branchExisted"`
	TmuxSession      string `json:"tmuxSession"`
	TmuxTarget       string `json:"tmuxTarget"`
	OpencodeLaunched bool   `json:"opencodeLaunched"`
}

// RemoveWorktreeRequest captures a remove-worktree action. Dir is any
// path inside the repo (used to resolve the repo root); Path is the
// worktree to remove.
type RemoveWorktreeRequest struct {
	Dir   string
	Path  string
	Force bool
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

// EnsureProjectOpencodeRequest asks the owning host to guarantee exactly
// one running opencode instance for the project containing ProjectDir
// (any path inside the repo). See spec/one-opencode-per-project D-1/D-4.
type EnsureProjectOpencodeRequest struct {
	ProjectDir string `json:"projectDir"`
}

// EnsureProjectOpencodeResult reports the discovered/launched opencode
// port, plus the resolved repo root and tmux session name for
// observability.
type EnsureProjectOpencodeResult struct {
	Port        string `json:"port"`
	RepoRoot    string `json:"repoRoot"`
	TmuxSession string `json:"tmuxSession"`
	Launched    bool   `json:"launched"`
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
// returned verbatim; handlers translate sentinel errors (git.ErrNotRepo,
// git.ErrNotARepo, ...) into HTTP status codes as before.
type Host interface {
	// RemoteID returns "local" for the in-process host, else the
	// remote's instance ID. Display/routing only.
	RemoteID() string

	// Capabilities reports which host operations are available.
	Capabilities() HostCaps

	// GitInfo returns per-directory git status for each dir.
	GitInfo(ctx context.Context, dirs []string) (map[string]git.Info, error)

	// GitDiff returns the working-tree diff for dir.
	GitDiff(ctx context.Context, dir string, opts GitDiffOptions) (*git.Diff, error)

	// GitBranches returns the local branch names for the repo
	// containing dir, current branch first.
	GitBranches(ctx context.Context, dir string) ([]string, error)

	// GitCheckout switches the working tree in dir to branch. Returns
	// git.ErrDirtyCheckout when git refuses due to local changes.
	GitCheckout(ctx context.Context, dir, branch string) error

	// ListWorktrees returns parsed `git worktree list` for the repo
	// containing dir.
	ListWorktrees(ctx context.Context, dir string) ([]git.Worktree, error)

	// WorktreeDefaultBaseRef returns the resolver's best-guess base ref
	// for new worktrees in the repo containing dir.
	WorktreeDefaultBaseRef(ctx context.Context, dir string) (string, error)

	// CreateWorktreeSession creates (or reuses) a worktree, ensures the
	// project's single opencode instance is running, and creates an
	// in-app session rooted at the worktree on that instance. Returns
	// the created session ID (#268). Runs on the owning host (R-C).
	CreateWorktreeSession(ctx context.Context, req WorktreeSessionRequest) (*WorktreeSessionResult, error)

	// RemoveWorktree removes the worktree at path from the repo
	// containing dir. With force, discards uncommitted changes. Runs on
	// the owning host (R-C).
	RemoveWorktree(ctx context.Context, req RemoveWorktreeRequest) error

	// LaunchTmux launches opencode in a tmux session for a directory.
	LaunchTmux(ctx context.Context, req LaunchTmuxRequest) (*LaunchTmuxResult, error)

	// EnsureProjectOpencode guarantees exactly one running opencode
	// instance for the project containing req.ProjectDir, rooted at the
	// project's main checkout. It discovers a running instance and, if
	// none exists, launches exactly one (seeding a scoped
	// external_directory permission) and waits for its port. Idempotent:
	// a second call returns the running instance and launches nothing.
	// Returns git.ErrNotARepo when the directory is not inside a repo.
	// Runs on the owning host (R-C).
	EnsureProjectOpencode(ctx context.Context, req EnsureProjectOpencodeRequest) (*EnsureProjectOpencodeResult, error)

	// TmuxSessions lists the host's tmux sessions.
	TmuxSessions(ctx context.Context) ([]TmuxSession, error)

	// Projects returns the host's known projects (its projects index).
	Projects(ctx context.Context) ([]db.ProjectStats, error)

	// TermWindows lists the in-app terminal windows for dir.
	TermWindows(ctx context.Context, dir string) ([]TermWindow, error)

	// TermCreateWindow creates a new terminal window for dir and returns
	// its name.
	TermCreateWindow(ctx context.Context, dir string) (string, error)

	// TermKillWindow kills the terminal window belonging to dir.
	TermKillWindow(ctx context.Context, dir, window string) error

	// TermAttach attaches an interactive PTY (on the owning host) to the
	// window selected by req and bridges it to conn until either side
	// closes. Runs on the owner (R-C): a remote terminal opens a shell on
	// the remote machine, not the hub.
	TermAttach(ctx context.Context, req TermAttachRequest, conn TermConn) error
}
