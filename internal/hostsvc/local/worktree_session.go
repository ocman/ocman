package local

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/git"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/opencode"
)

// CreateWorktreeSession runs on the owning host and uses the project's single
// managed OpenCode instance. Automatic names always create a fresh branch.
func (h *Host) CreateWorktreeSession(ctx context.Context, req hostsvc.WorktreeSessionRequest) (*hostsvc.WorktreeSessionResult, error) {
	repoRoot, err := git.ResolveRepoRoot(ctx, req.ProjectDir)
	if err != nil {
		return nil, err
	}
	if req.AutoName {
		ensured, err := h.EnsureProjectOpencode(ctx, hostsvc.EnsureProjectOpencodeRequest{ProjectDir: repoRoot})
		if err != nil {
			return nil, fmt.Errorf("ensuring project opencode: %w", err)
		}
		name, err := opencode.WorktreeName(ctx, ensured.Port(), repoRoot, req.Prompt)
		if err != nil {
			// ponytail: naming is best-effort; a random suffix still isolates every session.
			log.WithError(err).Warn("worktree: using fallback name")
			name = "session"
		}
		req.Branch = name + "-" + uuid.NewString()[:8]
		req.NewBranch = true
		req.MustCreateBranch = true
	}
	baseRef := req.BaseRef
	if req.NewBranch && baseRef == "" {
		baseRef = git.ResolveBaseRef(ctx, repoRoot)
	}
	res, err := git.CreateWorktree(ctx, git.CreateWorktreeRequest{
		RepoRoot: repoRoot, Branch: req.Branch, NewBranch: req.NewBranch,
		BaseRef: baseRef, MustCreateBranch: req.MustCreateBranch,
	})
	if err != nil {
		return nil, err
	}
	rollback := func() {
		if req.MustCreateBranch && !res.Reused && !res.BranchExisted {
			_ = git.RemoveWorktree(context.WithoutCancel(ctx), repoRoot, res.Path, true)
			_ = git.DeleteBranch(context.WithoutCancel(ctx), repoRoot, req.Branch)
		}
	}
	ensured, err := h.EnsureProjectOpencode(ctx, hostsvc.EnsureProjectOpencodeRequest{ProjectDir: repoRoot})
	if err != nil {
		rollback()
		return nil, fmt.Errorf("ensuring project opencode: %w", err)
	}
	if h.deps.CreateSession == nil {
		rollback()
		return nil, fmt.Errorf("CreateWorktreeSession: CreateSession dep not wired")
	}
	request := platforms.CreateSessionRequest{Directory: res.Path, Port: ensured.Port(), Title: req.Title}
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
	return &hostsvc.WorktreeSessionResult{
		SessionID: created.ID, WorktreePath: res.Path, Branch: res.Branch,
		Reused: res.Reused, BranchExisted: res.BranchExisted,
	}, nil
}
