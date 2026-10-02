package local

import (
	"context"
	"fmt"
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

	// The instance probe and the checkout are independent; overlap them.
	type ensureResult struct {
		res *hostsvc.EnsureProjectOpencodeResult
		err error
	}
	ensuredCh := make(chan ensureResult, 1)
	go func() {
		res, err := h.sfDoDetached(ctx, repoRoot, h.ensureLocked)
		ensuredCh <- ensureResult{res, err}
	}()

	baseRef := req.BaseRef
	if req.NewBranch && baseRef == "" {
		baseRef = git.ResolveBaseRef(ctx, repoRoot)
	}
	res, err := git.CreateWorktree(ctx, git.CreateWorktreeRequest{
		RepoRoot: repoRoot, Branch: req.Branch, NewBranch: req.NewBranch,
		BaseRef: baseRef, MustCreateBranch: req.MustCreateBranch,
	})
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
	port := ensured.res.Port()
	if h.deps.CreateSession == nil {
		rollback()
		return nil, fmt.Errorf("CreateWorktreeSession: CreateSession dep not wired")
	}
	request := platforms.CreateSessionRequest{Directory: res.Path, Port: port, Title: req.Title}
	if request.Title == "" {
		request.Title = req.Branch
	}
	var created *platforms.CreateSessionResponse
	if req.PermissionRules != nil {
		if h.deps.CreateConfiguredSession == nil {
			rollback()
			return nil, fmt.Errorf("CreateWorktreeSession: CreateConfiguredSession dep not wired")
		}
		created, err = h.deps.CreateConfiguredSession(ctx, request, req.PermissionRules)
	} else {
		created, err = h.deps.CreateSession(ctx, request)
	}
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
			h.nameWorktree(bg, port, repoRoot, created.ID, req.Branch, suffix, req.Title == "", req.Prompt)
		}()
	}
	return &hostsvc.WorktreeSessionResult{
		SessionID: created.ID, WorktreePath: res.Path, Branch: res.Branch,
		Reused: res.Reused, BranchExisted: res.BranchExisted,
	}, nil
}

// nameWorktree asks the small model for a descriptive name and renames the
// provisional branch (and the session title when it mirrors the branch).
// Best-effort: on any failure the provisional name simply stays.
func (h *Host) nameWorktree(ctx context.Context, port, repoRoot, sessionID, branch, suffix string, retitle bool, prompt string) {
	name, err := opencode.WorktreeName(ctx, port, repoRoot, prompt)
	if err != nil {
		log.WithError(err).Warn("worktree: keeping provisional name")
		return
	}
	renamed := name + "-" + suffix
	if err := git.RenameBranch(ctx, repoRoot, branch, renamed); err != nil {
		log.WithError(err).Warn("worktree: renaming provisional branch")
		return
	}
	if !retitle {
		return
	}
	if err := opencode.SetSessionTitle(ctx, port, sessionID, renamed); err != nil {
		log.WithError(err).Warn("worktree: renaming provisional session title")
	}
}
