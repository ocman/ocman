package local

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/git"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/opencode"
)

// provisionalPrefix names an automatic worktree until the small model has
// picked a descriptive name in the background.
const provisionalPrefix = "session-"

// CreateWorktreeSession runs on the owning host and uses the project's single
// managed OpenCode instance. Automatic names always create a fresh branch.
//
// Automatic names start provisional (session-<id8>) so the LLM naming call
// never sits on the first message's critical path; nameWorktree renames the
// branch and title afterwards while the worktree path stays put.
func (h *Host) CreateWorktreeSession(ctx context.Context, req hostsvc.WorktreeSessionRequest) (*hostsvc.WorktreeSessionResult, error) {
	repoRoot, err := git.ResolveRepoRoot(ctx, req.ProjectDir)
	if err != nil {
		return nil, err
	}
	suffix := ""
	if req.AutoName {
		suffix = uuid.NewString()[:8]
		req.Branch = provisionalPrefix + suffix
		req.NewBranch = true
		req.MustCreateBranch = true
	}

	baseRef := req.BaseRef
	if req.NewBranch && baseRef == "" {
		baseRef, err = git.ResolveBaseRef(ctx, repoRoot)
		if err != nil {
			return nil, err
		}
		if baseRef == "" {
			return nil, fmt.Errorf("worktree: repository has no usable commit; use the current checkout or create an initial commit")
		}
	}

	// The instance probe and the checkout are independent; overlap them.
	type ensureResult struct {
		res *hostsvc.EnsureProjectOpencodeResult
		err error
	}
	ensuredCh := make(chan ensureResult, 1)
	hostsvc.ReportProgress(ctx, hostsvc.StepOpencode, hostsvc.StepActive)
	go func() {
		res, err := h.EnsureProjectOpencode(ctx, hostsvc.EnsureProjectOpencodeRequest{ProjectDir: repoRoot})
		hostsvc.FinishStep(ctx, hostsvc.StepOpencode, err)
		ensuredCh <- ensureResult{res, err}
	}()

	hostsvc.ReportProgress(ctx, hostsvc.StepWorktree, hostsvc.StepActive)
	res, err := git.CreateWorktree(ctx, git.CreateWorktreeRequest{
		RepoRoot: repoRoot, Branch: req.Branch, NewBranch: req.NewBranch,
		BaseRef: baseRef, MustCreateBranch: req.MustCreateBranch,
	})
	hostsvc.FinishStep(ctx, hostsvc.StepWorktree, err)
	ensured := <-ensuredCh
	if err != nil {
		return nil, err
	}
	rollback := func() {
		if req.MustCreateBranch && !res.Reused && !res.BranchExisted {
			_ = git.RemoveWorktree(context.WithoutCancel(ctx), repoRoot, res.Path, true)
			_ = git.DeleteBranch(context.WithoutCancel(ctx), repoRoot, req.Branch)
		}
	}
	if ensured.err != nil {
		rollback()
		return nil, fmt.Errorf("ensuring project opencode: %w", ensured.err)
	}
	if !res.Reused {
		seedOpencodeDeps(ctx, repoRoot, res.Path)
	}
	port := ensured.res.Port()
	if h.deps.CreateSession == nil {
		rollback()
		return nil, fmt.Errorf("CreateWorktreeSession: CreateSession dep not wired")
	}
	request := platforms.CreateSessionRequest{Directory: res.Path, Port: port, Title: req.Title}
	// Automatic sessions keep OpenCode's default title so OpenCode titles
	// them from the first message; the branch name is no title.
	if request.Title == "" && !req.AutoName {
		request.Title = req.Branch
	}
	var created *platforms.CreateSessionResponse
	hostsvc.ReportProgress(ctx, hostsvc.StepSession, hostsvc.StepActive)
	if req.PermissionRules != nil {
		if h.deps.CreateConfiguredSession == nil {
			rollback()
			return nil, fmt.Errorf("CreateWorktreeSession: CreateConfiguredSession dep not wired")
		}
		created, err = h.deps.CreateConfiguredSession(ctx, request, req.PermissionRules)
	} else {
		created, err = h.deps.CreateSession(ctx, request)
	}
	hostsvc.FinishStep(ctx, hostsvc.StepSession, err)
	if err != nil {
		rollback()
		return nil, fmt.Errorf("creating worktree session: %w", err)
	}
	if req.AutoName {
		h.background.Add(1)
		go func() {
			defer h.background.Done()
			bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancel()
			h.nameWorktree(bg, port, repoRoot, req.Branch, suffix, req.Prompt)
		}()
	}
	return &hostsvc.WorktreeSessionResult{
		SessionID: created.ID, WorktreePath: res.Path, Branch: res.Branch,
		Reused: res.Reused, BranchExisted: res.BranchExisted,
	}, nil
}

// seedOpencodeDeps copies the main checkout's untracked .opencode
// dependencies into a fresh worktree. OpenCode runs an npm install for any
// config dir without node_modules and, with plugins configured, holds session
// creation on it (~2.5s); a present node_modules whose lock lists the plugin
// package is skipped. Best-effort: a failure only costs that install.
//
// Only a real, checked-out .opencode directory is seeded (a tracked symlink
// could redirect writes outside the worktree), existing entries are never
// replaced, and everything is staged first so a failed copy publishes nothing.
func seedOpencodeDeps(ctx context.Context, repoRoot, worktree string) {
	src, dst := filepath.Join(repoRoot, ".opencode"), filepath.Join(worktree, ".opencode")
	if _, err := os.Stat(filepath.Join(src, "node_modules")); err != nil {
		return
	}
	if fi, err := os.Lstat(dst); err != nil || !fi.IsDir() {
		return
	}
	var names []string
	for _, name := range []string{"node_modules", "package.json", "package-lock.json"} {
		_, srcErr := os.Stat(filepath.Join(src, name))
		_, dstErr := os.Lstat(filepath.Join(dst, name))
		if srcErr == nil && errors.Is(dstErr, fs.ErrNotExist) {
			names = append(names, name)
		}
	}
	if len(names) == 0 || names[0] != "node_modules" {
		return
	}
	stage, err := os.MkdirTemp(dst, ".ocman-seed-")
	if err != nil {
		return
	}
	defer func() { _ = os.RemoveAll(stage) }()
	for _, name := range names {
		// -c clones on APFS (falls back to a copy); -R keeps symlinks as links.
		args := []string{"-R", filepath.Join(src, name), filepath.Join(stage, name)}
		if runtime.GOOS == "darwin" {
			args = append([]string{"-c"}, args...)
		}
		if out, err := exec.CommandContext(ctx, "cp", args...).CombinedOutput(); err != nil {
			log.WithError(err).WithField("output", string(out)).Debug("worktree: seeding .opencode dependencies")
			return
		}
	}
	// node_modules lands first and the lock last: a tree without its lock
	// still makes OpenCode reinstall rather than trust it.
	for _, name := range names {
		if _, err := os.Lstat(filepath.Join(dst, name)); !errors.Is(err, fs.ErrNotExist) {
			return
		}
		if err := os.Rename(filepath.Join(stage, name), filepath.Join(dst, name)); err != nil {
			return
		}
	}
}

// nameWorktree asks the small model for a descriptive name and renames the
// provisional branch. Best-effort: on any failure the provisional name stays.
func (h *Host) nameWorktree(ctx context.Context, port, repoRoot, branch, suffix, prompt string) {
	name, err := opencode.WorktreeName(ctx, port, repoRoot, prompt)
	if err != nil {
		log.WithError(err).Warn("worktree: keeping provisional name")
		return
	}
	renamed := name + "-" + suffix
	if err := git.RenameBranch(ctx, repoRoot, branch, renamed); err != nil {
		log.WithError(err).Warn("worktree: renaming provisional branch")
	}
}
