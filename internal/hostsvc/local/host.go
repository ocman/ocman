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
	"fmt"
	"path/filepath"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/git"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
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
	//
	// Deprecated: no longer called from the /wt path (#268 runs worktree
	// sessions in-app on the project instance). Kept until #269 removes
	// the per-worktree tmux launcher machinery.
	LaunchWorktreeTmux func(projectDir, worktreeDir string) (string, bool, error)
	// CreateSession creates a session on the OpenCode platform. Injected
	// (rather than importing the adapter) so CreateWorktreeSession can
	// create the in-app worktree session on the project's single
	// opencode instance via the shared session-mutation code path.
	CreateSession func(ctx context.Context, req platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error)
	// LaunchProjectOpencode launches (idempotently) a single
	// `opencode --port 0` in a tmux session rooted at the project's main
	// checkout, seeding the pane with the given OPENCODE_PERMISSION JSON.
	// Returns the tmux session name. Used by EnsureProjectOpencode.
	LaunchProjectOpencode func(dir, permissionJSON string) (session string, err error)
	// DiscoverPort returns the HTTP port of a running opencode instance
	// whose working directory matches dir, or "" if none. Injected so the
	// local Host does not import the opencode adapter directly.
	DiscoverPort func(dir string) string
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

	// Port-discovery wait budget for EnsureProjectOpencode after a launch.
	// Exposed as fields (not consts) so tests can shrink them.
	portWaitTimeout  time.Duration
	portWaitInterval time.Duration
}

// New returns a local Host wired with the given server-package deps.
func New(deps Deps) *Host {
	return &Host{
		deps:             deps,
		portWaitTimeout:  15 * time.Second,
		portWaitInterval: 200 * time.Millisecond,
	}
}

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

// CreateWorktreeSession creates (or reuses) a git worktree, ensures the
// project's single opencode instance is running (#267), then creates an
// in-app session rooted at the worktree on that instance (#266/#268).
// No per-worktree tmux window is launched. Returns the created session ID.
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

	// Ensure the project's single opencode instance is running and get
	// its port. This is the only launch path; no per-worktree process.
	ensured, err := h.EnsureProjectOpencode(ctx, hostsvc.EnsureProjectOpencodeRequest{ProjectDir: repoRoot})
	if err != nil {
		return nil, fmt.Errorf("ensuring project opencode: %w", err)
	}

	// Create the session in-app on that instance, rooted at the worktree.
	if h.deps.CreateSession == nil {
		return nil, fmt.Errorf("CreateWorktreeSession: CreateSession dep not wired")
	}
	created, err := h.deps.CreateSession(ctx, platforms.CreateSessionRequest{
		Directory: res.Path,
		Port:      ensured.Port,
		Title:     req.Branch,
	})
	if err != nil {
		return nil, fmt.Errorf("creating worktree session: %w", err)
	}

	return &hostsvc.WorktreeSessionResult{
		SessionID:     created.ID,
		WorktreePath:  res.Path,
		Branch:        res.Branch,
		Reused:        res.Reused,
		BranchExisted: res.BranchExisted,
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
	// This runs on whichever host owns the directory: the hub for local
	// launches, the remote's own in-process Host for remote launches
	// (hub -> gRPC -> remote Server.LaunchTmux -> here). Log so the
	// launch is traceable on both sides.
	log.WithField("directory", req.Directory).Info("host: launching opencode in tmux")
	name, err := h.deps.LaunchTmux(req.Directory)
	if err != nil {
		log.WithError(err).WithField("directory", req.Directory).Error("host: failed to launch opencode in tmux")
		return nil, err
	}
	log.WithFields(log.Fields{"directory": req.Directory, "tmuxSession": name}).Info("host: launched opencode in tmux")
	return &hostsvc.LaunchTmuxResult{Session: name}, nil
}

// EnsureProjectOpencode guarantees exactly one opencode instance for the
// project containing req.ProjectDir, rooted at the project's main
// checkout. It is the only code path that launches opencode for a project
// (spec/one-opencode-per-project D-1/D-4).
func (h *Host) EnsureProjectOpencode(ctx context.Context, req hostsvc.EnsureProjectOpencodeRequest) (*hostsvc.EnsureProjectOpencodeResult, error) {
	repoRoot, err := git.ResolveRepoRoot(ctx, req.ProjectDir)
	if err != nil {
		return nil, err
	}

	discover := h.deps.DiscoverPort
	if discover == nil {
		discover = func(string) string { return "" }
	}

	// Already running: return it, launch nothing (idempotent).
	if port := discover(repoRoot); port != "" {
		return &hostsvc.EnsureProjectOpencodeResult{Port: port, RepoRoot: repoRoot}, nil
	}

	// Launch exactly one, seeded with a scoped external_directory rule for
	// this project's .worktrees/<repo> root.
	permJSON, err := buildExternalDirectoryPermission(worktreesRoot(repoRoot))
	if err != nil {
		return nil, fmt.Errorf("building OPENCODE_PERMISSION: %w", err)
	}
	session := ""
	if h.deps.LaunchProjectOpencode != nil {
		log.WithField("repoRoot", repoRoot).Info("host: launching project opencode")
		session, err = h.deps.LaunchProjectOpencode(repoRoot, permJSON)
		if err != nil {
			log.WithError(err).WithField("repoRoot", repoRoot).Error("host: failed to launch project opencode")
			return nil, err
		}
	}

	// Wait for the launched instance to bind its port.
	port, err := h.waitForPort(ctx, discover, repoRoot)
	if err != nil {
		return nil, err
	}
	return &hostsvc.EnsureProjectOpencodeResult{
		Port:        port,
		RepoRoot:    repoRoot,
		TmuxSession: session,
		Launched:    true,
	}, nil
}

// waitForPort polls discover until it returns a non-empty port, the
// context is cancelled, or the wait budget is exhausted.
func (h *Host) waitForPort(ctx context.Context, discover func(string) string, repoRoot string) (string, error) {
	deadline := time.Now().Add(h.portWaitTimeout)
	for {
		if port := discover(repoRoot); port != "" {
			return port, nil
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("opencode launched for %q but no port became discoverable within %s", repoRoot, h.portWaitTimeout)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(h.portWaitInterval):
		}
	}
}

// worktreesRoot returns the <repo-parent>/.worktrees/<repo-name> directory
// that in-app worktrees for this repo live under, mirroring
// git.WorktreePathFor's layout.
func worktreesRoot(repoRoot string) string {
	clean := filepath.Clean(repoRoot)
	return filepath.Join(filepath.Dir(clean), ".worktrees", filepath.Base(clean))
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
