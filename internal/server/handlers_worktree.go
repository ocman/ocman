package server

import (
	"errors"
	"net/http"
	"os/exec"
	"path/filepath"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/git"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/permissions"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/tmux"
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
	if !tmux.IsAvailable() {
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
//   - `dir` (required): absolute path inside a git git.
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
	dir, ok := parseAbsDir(w, r)
	if !ok {
		return
	}

	entries, err := s.router().ForDir(dir).ListWorktrees(r.Context(), dir)
	if err != nil {
		if errors.Is(err, git.ErrNotARepo) {
			http.Error(w, "directory is not a git repository", http.StatusNotFound)
			return
		}
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
	dir, ok := parseAbsDir(w, r)
	if !ok {
		return
	}

	baseRef, err := s.router().ForDir(dir).WorktreeDefaultBaseRef(r.Context(), dir)
	if err != nil {
		if errors.Is(err, git.ErrNotARepo) {
			http.Error(w, "directory is not a git repository", http.StatusNotFound)
			return
		}
		log.WithError(err).Warn("worktree: resolve base ref")
		http.Error(w, "failed to resolve repo root", http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]string{"baseRef": baseRef})
}

// handleWorktreeRemove removes a worktree via `git worktree remove`.
// git enforces the guards: it refuses the main checkout and refuses a
// dirty tree unless force is set.
//
// Localhost-only — same posture as create-and-launch, since this mutates
// the host filesystem.
//
// Request body: { projectDir, path, force?, remoteId? }
//
// Status codes:
//
//	400 — projectDir or path missing / relative
//	404 — projectDir is not a git repository
//	409 — target is the main worktree, or dirty (retry with force)
//	502 — git invocation failed
func (s *Server) handleWorktreeRemove(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ProjectDir string `json:"projectDir"`
		Path       string `json:"path"`
		Force      bool   `json:"force"`
		RemoteID   string `json:"remoteId"`
	}
	if !readAndUnmarshal(w, r, maxRequestBody, &req) {
		return
	}
	if req.ProjectDir == "" || req.Path == "" {
		http.Error(w, "projectDir and path are required", http.StatusBadRequest)
		return
	}
	if !filepath.IsAbs(req.ProjectDir) || !filepath.IsAbs(req.Path) {
		http.Error(w, "projectDir and path must be absolute paths", http.StatusBadRequest)
		return
	}

	host := s.router().ForDir(req.ProjectDir)
	if req.RemoteID != "" {
		host = s.router().ForRemote(req.RemoteID)
	}

	log.WithFields(log.Fields{
		"projectDir": req.ProjectDir,
		"path":       req.Path,
		"force":      req.Force,
		"remoteId":   req.RemoteID,
	}).Info("worktree: remove")

	err := host.RemoveWorktree(r.Context(), hostsvc.RemoveWorktreeRequest{
		Dir:   req.ProjectDir,
		Path:  req.Path,
		Force: req.Force,
	})
	if err != nil {
		switch {
		case errors.Is(err, git.ErrNotARepo):
			http.Error(w, "projectDir is not a git repository", http.StatusNotFound)
		case errors.Is(err, git.ErrMainWorktree),
			errors.Is(err, git.ErrWorktreeDirty):
			http.Error(w, err.Error(), http.StatusConflict)
		default:
			log.WithError(err).Warn("worktree: remove")
			http.Error(w, "worktree remove failed: "+err.Error(), http.StatusBadGateway)
		}
		return
	}
	writeJSON(w, map[string]bool{"removed": true})
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
	var req struct {
		ProjectDir string `json:"projectDir"`
		Branch     string `json:"branch"`
		NewBranch  bool   `json:"newBranch"`
		BaseRef    string `json:"baseRef"`
		// RemoteID, when set, runs the worktree create on the owning
		// remote host (FR-10/AD-16b). Empty / "local" = this machine.
		RemoteID string `json:"remoteId"`
		// ParentSessionID, when set + the worktree.inherit_permissions
		// setting is on, seeds the new session with the parent's
		// accumulated always-allow permissions (issue #101).
		ParentSessionID string `json:"parentSessionId"`
	}
	if !readAndUnmarshal(w, r, maxRequestBody, &req) {
		return
	}

	// git is a host-local precondition: creating a worktree always needs
	// it. Only enforce on the hub when the action targets this machine; a
	// remote target validates its own tooling on its side.
	//
	// tmux is NOT gated up-front: worktree sessions now run in-app on the
	// project's single opencode instance (#268). When that instance is
	// already running, /wt needs no tmux at all. tmux is only required to
	// *launch* an instance — EnsureProjectOpencode surfaces a clear error
	// (HTTP 502) in that case if tmux is missing.
	local := req.RemoteID == "" || req.RemoteID == "local"
	if local {
		if _, err := exec.LookPath("git"); err != nil {
			http.Error(w, "git is not available", http.StatusServiceUnavailable)
			return
		}
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

	log.WithFields(log.Fields{
		"projectDir": req.ProjectDir,
		"branch":     req.Branch,
		"newBranch":  req.NewBranch,
		"baseRef":    req.BaseRef,
		"remoteId":   req.RemoteID,
	}).Info("worktree: create and launch")

	// Resolve the owning host: ForRemote when the caller named the owner
	// (preferred, AD-16b), else ForDir inference for backward-compatible
	// local behaviour.
	host := s.router().ForDir(req.ProjectDir)
	if req.RemoteID != "" {
		host = s.router().ForRemote(req.RemoteID)
	}
	res, err := host.CreateWorktreeSession(r.Context(), hostsvc.WorktreeSessionRequest{
		ProjectDir: req.ProjectDir,
		Branch:     req.Branch,
		NewBranch:  req.NewBranch,
		BaseRef:    req.BaseRef,
	})
	if err != nil {
		switch {
		case errors.Is(err, git.ErrNotARepo):
			http.Error(w, "projectDir is not a git repository", http.StatusNotFound)
		case errors.Is(err, git.ErrBranchCheckedOutElsewhere),
			errors.Is(err, git.ErrPathConflict):
			http.Error(w, err.Error(), http.StatusConflict)
		default:
			log.WithError(err).Warn("worktree: create and launch")
			http.Error(w, "worktree create/launch failed: "+err.Error(), http.StatusBadGateway)
		}
		return
	}

	// Seed the new worktree session with the parent's accumulated
	// always-allow permissions (issue #101). Soft-fail: never turn a
	// successful worktree launch into an error; surface any problem via
	// the response fields instead.
	inheritedCount, inheritErr := s.applyInheritedPermissions(r, req.ParentSessionID, res.SessionID)

	writeJSON(w, map[string]interface{}{
		"sessionId":                 res.SessionID,
		"worktreePath":              res.WorktreePath,
		"branch":                    res.Branch,
		"reused":                    res.Reused,
		"branchExisted":             res.BranchExisted,
		"permissionsInherited":      inheritedCount > 0,
		"permissionsInheritedCount": inheritedCount,
		"permissionsInheritError":   inheritErr,
	})
}

// applyInheritedPermissions builds the parent's always-allow ruleset
// and applies it to the freshly-created worktree session when the
// worktree.inherit_permissions setting is on and a parent was named.
// Returns the number of rules applied and a soft-fail note (empty on
// success or when inheritance was skipped). Never blocks the launch.
func (s *Server) applyInheritedPermissions(r *http.Request, parentSessionID, childSessionID string) (int, string) {
	if s.stateDB == nil || parentSessionID == "" || childSessionID == "" {
		return 0, ""
	}
	on, err := s.stateDB.GetWorktreeInheritPermissions()
	if err != nil {
		log.WithError(err).Warn("worktree: reading inherit-permissions setting")
		return 0, "reading setting: " + err.Error()
	}
	if !on {
		return 0, ""
	}
	// The /wt flow is OpenCode-only (AD-7) and doesn't pass ?platform=;
	// approvals are recorded under the platform id, so default to
	// "opencode" when no explicit hint is present.
	platform := platformHint(r)
	if platform == "" {
		platform = "opencode"
	}
	rules, count, err := permissions.BuildInheritedRules(s.stateDB, platform, parentSessionID)
	if err != nil {
		log.WithError(err).Warn("worktree: building inherited permission rules")
		return 0, "building rules: " + err.Error()
	}
	if count == 0 {
		return 0, ""
	}
	if err := s.sessions.SetPermissionRules(r.Context(), platform, platforms.SetPermissionRulesRequest{
		SessionID: childSessionID,
		Rules:     rules,
	}); err != nil {
		log.WithError(err).Warn("worktree: applying inherited permission rules")
		return 0, "applying rules: " + err.Error()
	}
	return count, ""
}
