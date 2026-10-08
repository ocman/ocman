// Package local implements hostsvc.Host for the in-process machine. Directory
// operations run here; server-owned services are injected to avoid import cycles.
package local

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/git"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/ocapi"
	"github.com/NoUseFreak/ocman/internal/ocruntime"
	"github.com/NoUseFreak/ocman/internal/platforms"
	log "github.com/sirupsen/logrus"
	"golang.org/x/sync/singleflight"
)

// Deps injects server-owned services and runtime seams into the local host.
type Deps struct {
	LaunchTmux              func(context.Context, string) (string, error)
	CreateSession           func(context.Context, platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error)
	CreateConfiguredSession func(context.Context, platforms.CreateSessionRequest, []platforms.PermissionRule) (*platforms.CreateSessionResponse, error)
	Runtime                 ocruntime.Runtime
	// BeforeReplace prepares history, BeforeStop records the stop boundary,
	// and AfterStop confirms reconciliation.
	BeforeReplace         func(context.Context, string, string) error
	BeforeStop            func(context.Context, string, *ocruntime.Instance) error
	AfterStop             func(context.Context, string) error
	ReplacementStopped    func(context.Context, string) (bool, error)
	ReplacementStopping   func(context.Context, string) (*ocruntime.Instance, error)
	CancelReplacementStop func(context.Context, string) error
	DiscoverPort          func(string) string
	SetMachineServer      func(string)
	ManagedStore          ManagedStore
	OpenCodeAuth          func() ocapi.Auth
	OpenCodeReloaded      func(port string)
	TmuxSessions          func(context.Context) ([]hostsvc.TmuxSession, error)
	Projects              func(context.Context) ([]db.ProjectStats, error)
	ProjectUpstreams      func(context.Context, string) (*hostsvc.ProjectUpstreams, error)
	FetchPRHead           func(context.Context, hostsvc.FetchPRHeadRequest) (string, error)
	Caps                  func() hostsvc.HostCaps
	TermWindows           func(context.Context, string) ([]hostsvc.TermWindow, error)
	TermCreateWindow      func(context.Context, string) (string, error)
	TermKillWindow        func(context.Context, string, string) error
	TermAttach            func(context.Context, hostsvc.TermAttachRequest, hostsvc.TermConn) error
	StateDir              string
}

// ManagedInstance mirrors the durable runtime fields needed for recovery.
type ManagedInstance struct {
	Endpoint   string
	Kind       string
	RuntimeID  string
	PID        int
	LaunchedAt time.Time
}

type ManagedStore interface {
	Upsert(context.Context, string, ManagedInstance) error
	Get(context.Context, string) (ManagedInstance, bool, error)
	Delete(context.Context, string) error
	List(context.Context) (map[string]ManagedInstance, error)
}

type Host struct {
	deps    Deps
	runtime ocruntime.Runtime
	store   ManagedStore
	// sf collapses concurrent runtime operations for the same repo root.
	sf singleflight.Group
	// background tracks detached worktree naming so tests can wait for it.
	background sync.WaitGroup
	// instances retains cleanup handles; only successful probes authorize routing.
	mu         sync.Mutex
	instances  map[string]*ocruntime.Instance
	authorized map[string]string
	// stopped retains non-routable recovery until reconciliation commits.
	stopped          map[string]bool
	portWaitTimeout  time.Duration
	portWaitInterval time.Duration
}

func New(deps Deps) *Host {
	rt := deps.Runtime
	if rt == nil {
		rt = ocruntime.NewNativeRuntime()
	}
	return &Host{
		deps: deps, runtime: rt, store: deps.ManagedStore,
		instances:       map[string]*ocruntime.Instance{},
		authorized:      map[string]string{},
		stopped:         map[string]bool{},
		portWaitTimeout: 15 * time.Second, portWaitInterval: 200 * time.Millisecond,
	}
}

func (h *Host) ValidateFactoryHandoff(ctx context.Context, repoRoot, branch string) (string, error) {
	return git.ValidateFactoryHandoff(ctx, repoRoot, branch)
}
func (h *Host) PrepareFactoryWorkspace(ctx context.Context, repoRoot, branch, checkpoint, target string) (string, string, error) {
	return git.PrepareFactoryWorkspace(ctx, repoRoot, branch, checkpoint, target)
}
func (h *Host) ValidateFactoryCheckpoint(ctx context.Context, repoRoot, branch, checkpoint string) (string, error) {
	return git.ValidateFactoryCheckpoint(ctx, repoRoot, branch, checkpoint)
}

// ReadFile keeps the target inside dir, including after symlink resolution.
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
func (h *Host) ListRepoFiles(ctx context.Context, dir string, ignored bool) (*git.FileList, error) {
	return git.ListFiles(ctx, dir, ignored)
}
func (h *Host) ReadRepoFile(ctx context.Context, dir, path string, ignored bool) (*git.FileContent, error) {
	return git.ReadFile(ctx, dir, path, ignored)
}
func (h *Host) GitCheckout(ctx context.Context, dir, branch string) error {
	return git.Checkout(ctx, dir, branch)
}
func (h *Host) ProjectUpstreams(ctx context.Context, dir string) (*hostsvc.ProjectUpstreams, error) {
	return h.deps.ProjectUpstreams(ctx, dir)
}
func (h *Host) FetchPRHead(ctx context.Context, req hostsvc.FetchPRHeadRequest) (string, error) {
	return h.deps.FetchPRHead(ctx, req)
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
	return git.ResolveBaseRef(ctx, repoRoot)
}
func (h *Host) RemoveWorktree(ctx context.Context, req hostsvc.RemoveWorktreeRequest) error {
	repoRoot, err := git.ResolveRepoRoot(ctx, req.Dir)
	if err != nil {
		return err
	}
	return git.RemoveWorktree(ctx, repoRoot, req.Path, req.Force)
}
func (h *Host) LaunchTmux(ctx context.Context, req hostsvc.LaunchTmuxRequest) (*hostsvc.LaunchTmuxResult, error) {
	log.WithField("directory", req.Directory).Info("host: launching opencode in tmux")
	name, err := h.deps.LaunchTmux(ctx, req.Directory)
	if err != nil {
		log.WithError(err).WithField("directory", req.Directory).Error("host: failed to launch opencode in tmux")
		return nil, err
	}
	log.WithFields(log.Fields{"directory": req.Directory, "tmuxSession": name}).Info("host: launched opencode in tmux")
	return &hostsvc.LaunchTmuxResult{Session: name}, nil
}
func (h *Host) TmuxSessions(ctx context.Context) ([]hostsvc.TmuxSession, error) {
	if h.deps.TmuxSessions == nil {
		return nil, nil
	}
	return h.deps.TmuxSessions(ctx)
}
func (h *Host) Projects(ctx context.Context) ([]db.ProjectStats, error) {
	if h.deps.Projects == nil {
		return nil, nil
	}
	return h.deps.Projects(ctx)
}
func (h *Host) TermWindows(ctx context.Context, dir string) ([]hostsvc.TermWindow, error) {
	if h.deps.TermWindows == nil {
		return nil, nil
	}
	return h.deps.TermWindows(ctx, dir)
}
func (h *Host) TermCreateWindow(ctx context.Context, dir string) (string, error) {
	if h.deps.TermCreateWindow == nil {
		return "", nil
	}
	return h.deps.TermCreateWindow(ctx, dir)
}
func (h *Host) TermKillWindow(ctx context.Context, dir, window string) error {
	if h.deps.TermKillWindow == nil {
		return nil
	}
	return h.deps.TermKillWindow(ctx, dir, window)
}
func (h *Host) TermAttach(ctx context.Context, req hostsvc.TermAttachRequest, conn hostsvc.TermConn) error {
	if h.deps.TermAttach == nil {
		return nil
	}
	return h.deps.TermAttach(ctx, req, conn)
}
