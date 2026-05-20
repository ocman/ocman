package server

import (
	"errors"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/worktree"
)

// worktreeSessionsAvailable reports whether all preconditions for the
// /wt feature are met:
//   - tmux on PATH (existing tmux integration)
//   - git on PATH
//   - opencode on PATH (the agent we launch into the worktree)
//   - at least one OpenCode platform adapter registered (v1 is
//     OpenCode-only; AD-7)
//
// Surfaced via /api/capabilities as a top-level boolean. The frontend
// uses it to gate UI affordances without branching on platform identity.
func worktreeSessionsAvailable(reg *platforms.Registry) bool {
	if !isTmuxAvailable() {
		return false
	}
	if _, err := exec.LookPath("git"); err != nil {
		return false
	}
	if _, err := exec.LookPath("opencode"); err != nil {
		return false
	}
	for _, p := range reg.Platforms() {
		if p.ID() == "opencode" {
			return true
		}
	}
	return false
}

// handleWorktreeList responds with the parsed `git worktree list
// --porcelain` output for the repo containing dir.
//
// Query parameters:
//   - `dir` (required): absolute path inside a git worktree.
//
// Response 200:
//
//	{ "worktrees": [{path, branch, head, bare, locked, main}, ...] }
//
// Status codes:
//
//	400 — `dir` missing or relative
//	404 — `dir` is not inside a git repo
//	502 — git invocation failed
func (s *Server) handleWorktreeList(w http.ResponseWriter, r *http.Request) {
	dir := r.URL.Query().Get("dir")
	if dir == "" {
		http.Error(w, "dir query parameter is required", http.StatusBadRequest)
		return
	}
	if !filepath.IsAbs(dir) {
		http.Error(w, "dir must be an absolute path", http.StatusBadRequest)
		return
	}

	repoRoot, err := worktree.ResolveRepoRoot(r.Context(), dir)
	if err != nil {
		if errors.Is(err, worktree.ErrNotARepo) {
			http.Error(w, "directory is not a git repository", http.StatusNotFound)
			return
		}
		log.WithError(err).Warn("worktree: resolve repo root")
		http.Error(w, "failed to resolve repo root", http.StatusBadGateway)
		return
	}

	entries, err := worktree.List(r.Context(), repoRoot)
	if err != nil {
		log.WithError(err).Warn("worktree: list")
		http.Error(w, "git worktree list failed", http.StatusBadGateway)
		return
	}

	writeJSON(w, map[string]interface{}{"worktrees": entries})
}

// handleWorktreeDefaultBaseRef returns the resolver's best guess for
// the default base ref (AD-5). Used by the frontend to pre-fill the
// "base ref" field in the worktree-creation form.
func (s *Server) handleWorktreeDefaultBaseRef(w http.ResponseWriter, r *http.Request) {
	dir := r.URL.Query().Get("dir")
	if dir == "" {
		http.Error(w, "dir query parameter is required", http.StatusBadRequest)
		return
	}
	if !filepath.IsAbs(dir) {
		http.Error(w, "dir must be an absolute path", http.StatusBadRequest)
		return
	}

	repoRoot, err := worktree.ResolveRepoRoot(r.Context(), dir)
	if err != nil {
		if errors.Is(err, worktree.ErrNotARepo) {
			http.Error(w, "directory is not a git repository", http.StatusNotFound)
			return
		}
		log.WithError(err).Warn("worktree: resolve repo root")
		http.Error(w, "failed to resolve repo root", http.StatusBadGateway)
		return
	}

	baseRef := worktree.ResolveBaseRef(r.Context(), repoRoot)
	writeJSON(w, map[string]string{"baseRef": baseRef})
}

// handleWorktreeCreateAndLaunch creates a worktree (or reuses an
// existing one) and launches `opencode --port 0` inside a tmux session
// rooted at the worktree path. Idempotent: re-running with the same
// branch on the same project lands the user back in their existing
// tmux session without spawning a second opencode (AD-4).
//
// Localhost-only — same security posture as the existing tmux launch
// endpoint, since this spawns processes on the host.
func (s *Server) handleWorktreeCreateAndLaunch(w http.ResponseWriter, r *http.Request) {
	if !isTmuxAvailable() {
		http.Error(w, "tmux is not available", http.StatusServiceUnavailable)
		return
	}
	if _, err := exec.LookPath("git"); err != nil {
		http.Error(w, "git is not available", http.StatusServiceUnavailable)
		return
	}

	var req struct {
		ProjectDir string `json:"projectDir"`
		Branch     string `json:"branch"`
		NewBranch  bool   `json:"newBranch"`
		BaseRef    string `json:"baseRef"`
	}
	if !readAndUnmarshal(w, r, maxRequestBody, &req) {
		return
	}
	if req.ProjectDir == "" {
		http.Error(w, "projectDir is required", http.StatusBadRequest)
		return
	}
	if !filepath.IsAbs(req.ProjectDir) {
		http.Error(w, "projectDir must be an absolute path", http.StatusBadRequest)
		return
	}
	if req.Branch == "" {
		http.Error(w, "branch is required", http.StatusBadRequest)
		return
	}
	if req.NewBranch && req.BaseRef == "" {
		http.Error(w, "baseRef is required when newBranch is true", http.StatusBadRequest)
		return
	}

	// Resolve the repo root so users can pass any directory inside
	// the project (matches the gitinfo / git diff endpoints).
	repoRoot, err := worktree.ResolveRepoRoot(r.Context(), req.ProjectDir)
	if err != nil {
		if errors.Is(err, worktree.ErrNotARepo) {
			http.Error(w, "projectDir is not a git repository", http.StatusNotFound)
			return
		}
		log.WithError(err).Warn("worktree: resolve repo root")
		http.Error(w, "failed to resolve repo root", http.StatusBadGateway)
		return
	}

	log.WithFields(log.Fields{
		"repo":      repoRoot,
		"branch":    req.Branch,
		"newBranch": req.NewBranch,
		"baseRef":   req.BaseRef,
	}).Info("worktree: create and launch")

	res, err := worktree.Create(r.Context(), worktree.CreateRequest{
		RepoRoot:  repoRoot,
		Branch:    req.Branch,
		NewBranch: req.NewBranch,
		BaseRef:   req.BaseRef,
	})
	if err != nil {
		switch {
		case errors.Is(err, worktree.ErrBranchCheckedOutElsewhere),
			errors.Is(err, worktree.ErrPathConflict):
			http.Error(w, err.Error(), http.StatusConflict)
		default:
			log.WithError(err).Warn("worktree: create")
			http.Error(w, "git worktree add failed: "+err.Error(), http.StatusBadGateway)
		}
		return
	}

	tmuxTarget, launched, err := launchOpencodeInProjectTmuxWindow(repoRoot, res.Path)
	if err != nil {
		log.WithError(err).Error("worktree: tmux launch")
		http.Error(w, "tmux launch failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	tmuxSession, _, _ := strings.Cut(tmuxTarget, ":")

	writeJSON(w, map[string]interface{}{
		"worktreePath":     res.Path,
		"branch":           res.Branch,
		"reused":           res.Reused,
		"branchExisted":    res.BranchExisted,
		"tmuxSession":      tmuxSession,
		"tmuxTarget":       tmuxTarget,
		"opencodeLaunched": launched,
	})
}
