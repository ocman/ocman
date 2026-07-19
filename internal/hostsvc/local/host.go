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
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	"golang.org/x/sync/singleflight"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/git"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/ocruntime"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

// Deps carries the host operations that live in the server package
// (tmux, projects, whisper availability). They are injected so this
// package does not import server.
type Deps struct {
	// LaunchTmux runs `opencode --port 0` in a tmux session for the
	// directory, returning the session name.
	LaunchTmux func(directory string) (string, error)
	// CreateSession creates a session on the OpenCode platform. Injected
	// (rather than importing the adapter) so CreateWorktreeSession can
	// create the in-app worktree session on the project's single
	// opencode instance via the shared session-mutation code path.
	CreateSession func(ctx context.Context, req platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error)
	// Runtime hosts the project's managed OpenCode instance
	// (launch/probe/stop). Nil defaults to the native tmux runtime, which
	// reproduces ocman's historical behaviour (one opencode per project
	// in a tmux session). Injected so tests can drive EnsureProjectOpencode
	// against a fake Runtime and a container runtime can land later (#375).
	Runtime ocruntime.Runtime
	// DiscoverPort returns the port of an already-running OpenCode server
	// rooted at the project, or "" when none exists.
	DiscoverPort func(directory string) string
	// ManagedStore persists managed-instance records so a project's
	// opencode survives an ocman restart (#391, AD-5). Optional: when nil
	// the host falls back to the in-memory map alone. When present, the
	// host always re-probes a persisted row before trusting it — healthy
	// reuses, dead is discarded and relaunched.
	ManagedStore ManagedStore
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

// ManagedInstance is the host's view of a persisted managed instance.
// It mirrors the fields the host needs to reconstruct an
// ocruntime.Instance for a re-probe after a restart. The state layer
// stores an equivalent plain struct (state.ManagedInstance) so it stays
// decoupled from internal/ocruntime; the server converts at the wiring
// seam.
type ManagedInstance struct {
	Endpoint   string
	Kind       string
	RuntimeID  string
	PID        int
	LaunchedAt time.Time
}

// ManagedStore persists managed-instance records keyed by canonical repo
// root. Backed by internal/state in production; a fake in tests.
type ManagedStore interface {
	Upsert(repoRoot string, inst ManagedInstance) error
	Get(repoRoot string) (ManagedInstance, bool, error)
	Delete(repoRoot string) error
}

// Host is the local hostsvc.Host implementation.
type Host struct {
	deps    Deps
	runtime ocruntime.Runtime
	store   ManagedStore

	// sf collapses concurrent EnsureProjectOpencode calls for the same
	// repo root into a single launch (AD-9). ensure keys on repoRoot.
	sf singleflight.Group

	// instances is the in-memory managed-instance registry, keyed by repo
	// root. It is a hot cache in front of the persisted store (#391): when
	// a store is wired, recovery works from the store alone on a fresh
	// Host (empty map), and every successful launch upserts the store row.
	// With no store the map is the sole registry (unchanged legacy path).
	// ponytail: map is a cache backed by ManagedStore; #392 restart-clear
	// should invalidate both the map and the store row.
	mu        sync.Mutex
	instances map[string]*ocruntime.Instance

	// Health wait budget for EnsureProjectOpencode after a launch.
	// Exposed as fields (not consts) so tests can shrink them.
	portWaitTimeout  time.Duration
	portWaitInterval time.Duration
}

// New returns a local Host wired with the given server-package deps. When
// deps.Runtime is nil it defaults to the native tmux runtime.
func New(deps Deps) *Host {
	rt := deps.Runtime
	if rt == nil {
		rt = ocruntime.NewNativeRuntime()
	}
	return &Host{
		deps:             deps,
		runtime:          rt,
		store:            deps.ManagedStore,
		instances:        map[string]*ocruntime.Instance{},
		portWaitTimeout:  15 * time.Second,
		portWaitInterval: 200 * time.Millisecond,
	}
}

// ReadFile reads a file while keeping it inside the requested directory,
// including after symlink resolution.
func (h *Host) ReadFile(_ context.Context, dir, name string) ([]byte, error) {
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, err
	}
	target, err := filepath.EvalSymlinks(filepath.Join(root, name))
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("path escapes requested directory")
	}
	return os.ReadFile(target)
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
		Port:      ensured.Port(),
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

	// singleflight on repoRoot: overlapping ensure calls for one project
	// collapse into one launch; every caller gets the same result (AD-9).
	v, err, _ := h.sf.Do(repoRoot, func() (any, error) {
		return h.ensureLocked(ctx, repoRoot)
	})
	if err != nil {
		return nil, err
	}
	return v.(*hostsvc.EnsureProjectOpencodeResult), nil
}

// RestartProjectOpencode stops the project's currently tracked managed
// instance (if any) then re-ensures it, launching a fresh one (AD-7). It
// runs under the SAME singleflight key as EnsureProjectOpencode so a
// restart cannot race an ensure. It calls the inner restartLocked body
// directly (never EnsureProjectOpencode, which would sf.Do the same key
// and deadlock).
func (h *Host) RestartProjectOpencode(ctx context.Context, req hostsvc.EnsureProjectOpencodeRequest) (*hostsvc.EnsureProjectOpencodeResult, error) {
	repoRoot, err := git.ResolveRepoRoot(ctx, req.ProjectDir)
	if err != nil {
		return nil, err
	}
	v, err, _ := h.sf.Do(repoRoot, func() (any, error) {
		return h.restartLocked(ctx, repoRoot)
	})
	if err != nil {
		return nil, err
	}
	return v.(*hostsvc.EnsureProjectOpencodeResult), nil
}

// ensureLocked is the guarded body run under singleflight for one repo
// root: reuse the current instance when Probe says it's healthy, else
// launch a fresh one via the runtime and wait for it to serve.
func (h *Host) ensureLocked(ctx context.Context, repoRoot string) (*hostsvc.EnsureProjectOpencodeResult, error) {
	// Reuse candidate: the in-memory cache, or (on a fresh Host after a
	// restart) a persisted store row. Either way it must Probe healthy
	// before we trust it (AD-5). A dead persisted row is discarded so the
	// launch path below relaunches.
	if inst := h.reuseCandidate(repoRoot); inst != nil {
		if h.runtime.Probe(ctx, inst) {
			h.setInstance(repoRoot, inst)
			return &hostsvc.EnsureProjectOpencodeResult{
				Endpoint: inst.Endpoint,
				RepoRoot: repoRoot,
				Runtime:  *inst,
			}, nil
		}
		// Dead -> drop the persisted row so a stale endpoint can't be
		// reused after this relaunch.
		if h.store != nil {
			if err := h.store.Delete(repoRoot); err != nil {
				log.WithError(err).WithField("repoRoot", repoRoot).Warn("host: deleting stale managed opencode row")
			}
		}
	}

	// Adopt servers that predate the managed registry (or were started
	// outside ocman) before launching another instance for the project.
	if h.deps.DiscoverPort != nil {
		if port := h.deps.DiscoverPort(repoRoot); port != "" {
			inst := &ocruntime.Instance{
				Endpoint: "http://127.0.0.1:" + port,
				Kind:     ocruntime.KindNativeTmux,
			}
			if h.runtime.Probe(ctx, inst) {
				h.setInstance(repoRoot, inst)
				return &hostsvc.EnsureProjectOpencodeResult{
					Endpoint: inst.Endpoint,
					RepoRoot: repoRoot,
					Runtime:  *inst,
				}, nil
			}
		}
	}

	// Absent or stale (Probe false) -> launch a fresh one.
	return h.launchAndTrack(ctx, repoRoot)
}

// restartLocked is the guarded body for RestartProjectOpencode, run under
// the same singleflight key as ensureLocked. It stops the tracked instance
// (soft-fail), clears both the in-memory cache entry and the persisted
// store row (soft-fail), then launches a fresh one via launchAndTrack.
func (h *Host) restartLocked(ctx context.Context, repoRoot string) (*hostsvc.EnsureProjectOpencodeResult, error) {
	if inst := h.reuseCandidate(repoRoot); inst != nil {
		// Soft-fail: we're relaunching regardless, so a Stop error must
		// not block the restart.
		if err := h.runtime.Stop(ctx, inst); err != nil {
			log.WithError(err).WithField("repoRoot", repoRoot).Warn("host: stopping managed opencode for restart")
		}
	}
	// Clear the hot cache entry so a stale endpoint can't be reused.
	h.clearInstance(repoRoot)
	// Clear the persisted row too (soft-fail): the fresh launch re-upserts.
	if h.store != nil {
		if err := h.store.Delete(repoRoot); err != nil {
			log.WithError(err).WithField("repoRoot", repoRoot).Warn("host: deleting managed opencode row for restart")
		}
	}
	return h.launchAndTrack(ctx, repoRoot)
}

// launchAndTrack allocates a port, launches a fresh opencode instance
// (seeded with a scoped external_directory rule for this project's
// .worktrees/<repo> root), tracks it in the cache, waits for it to serve,
// persists it, and returns the result with Launched=true. Shared by
// ensureLocked (cold/stale path) and restartLocked.
func (h *Host) launchAndTrack(ctx context.Context, repoRoot string) (*hostsvc.EnsureProjectOpencodeResult, error) {
	permJSON, err := buildExternalDirectoryPermission(worktreesRoot(repoRoot))
	if err != nil {
		return nil, fmt.Errorf("building OPENCODE_PERMISSION: %w", err)
	}
	port, err := ocruntime.AllocateLoopbackPort()
	if err != nil {
		return nil, err
	}
	log.WithFields(log.Fields{"repoRoot": repoRoot, "port": port}).Info("host: launching project opencode")
	inst, err := h.runtime.Launch(ctx, ocruntime.LaunchSpec{
		RepoRoot:       repoRoot,
		Host:           "127.0.0.1",
		Port:           port,
		PermissionJSON: permJSON,
	})
	if err != nil {
		log.WithError(err).WithField("repoRoot", repoRoot).Error("host: failed to launch project opencode")
		return nil, err
	}
	h.setInstance(repoRoot, inst)

	// Wait for the launched instance to actually serve the OpenCode API.
	if err := h.waitForProbe(ctx, inst); err != nil {
		return nil, err
	}

	// Persist so the instance survives a restart. Soft-fail: a store error
	// must not break an otherwise-successful launch.
	if h.store != nil {
		mi := ManagedInstance{
			Endpoint:   inst.Endpoint,
			Kind:       inst.Kind,
			RuntimeID:  inst.ID,
			PID:        inst.PID,
			LaunchedAt: time.Now(),
		}
		if err := h.store.Upsert(repoRoot, mi); err != nil {
			log.WithError(err).WithField("repoRoot", repoRoot).Warn("host: persisting managed opencode row")
		}
	}
	return &hostsvc.EnsureProjectOpencodeResult{
		Endpoint: inst.Endpoint,
		RepoRoot: repoRoot,
		Runtime:  *inst,
		Launched: true,
	}, nil
}

func (h *Host) currentInstance(repoRoot string) *ocruntime.Instance {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.instances[repoRoot]
}

// reuseCandidate returns the instance to re-probe for reuse: the hot
// in-memory cache first, then (on a fresh Host after a restart) the
// persisted store row. Returns nil when neither has an entry.
func (h *Host) reuseCandidate(repoRoot string) *ocruntime.Instance {
	if inst := h.currentInstance(repoRoot); inst != nil {
		return inst
	}
	if h.store == nil {
		return nil
	}
	mi, ok, err := h.store.Get(repoRoot)
	if err != nil {
		log.WithError(err).WithField("repoRoot", repoRoot).Warn("host: reading persisted managed opencode row")
		return nil
	}
	if !ok {
		return nil
	}
	return &ocruntime.Instance{Endpoint: mi.Endpoint, Kind: mi.Kind, ID: mi.RuntimeID, PID: mi.PID}
}

func (h *Host) setInstance(repoRoot string, inst *ocruntime.Instance) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.instances[repoRoot] = inst
}

func (h *Host) clearInstance(repoRoot string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.instances, repoRoot)
}

// waitForProbe polls Runtime.Probe until it reports healthy, the context
// is cancelled, or the wait budget is exhausted.
func (h *Host) waitForProbe(ctx context.Context, inst *ocruntime.Instance) error {
	deadline := time.Now().Add(h.portWaitTimeout)
	for {
		if h.runtime.Probe(ctx, inst) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("opencode launched for %q but did not become healthy within %s", inst.Endpoint, h.portWaitTimeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
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
