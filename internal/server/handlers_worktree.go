package server

import (
	"errors"
	"net/http"
	"os/exec"
	"path/filepath"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/git"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
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
//   - `remoteId` (optional): explicit owner; fails closed (503) when it
//     is not connected. Empty falls back to directory inference.
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

	host, ok := s.resolveOwner(w, dir, r.URL.Query().Get("remoteId"))
	if !ok {
		return
	}
	entries, err := host.ListWorktrees(r.Context(), dir)
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

	host, ok := s.resolveOwner(w, dir, r.URL.Query().Get("remoteId"))
	if !ok {
		return
	}
	baseRef, err := host.WorktreeDefaultBaseRef(r.Context(), dir)
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

	// Fail closed on an explicit owner: a stale/disconnected remote ID
	// must not degrade to the hub, which would `git worktree remove
	// --force` the hub's identically-pathed worktree and destroy real work.
	host, ok := s.resolveOwner(w, req.ProjectDir, req.RemoteID)
	if !ok {
		return
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
		AutoName   bool   `json:"autoName"`
		Prompt     string `json:"prompt"`
		// RemoteID, when set, runs the worktree create on the owning
		// remote host (FR-10/AD-16b). Empty / "local" = this machine.
		RemoteID string `json:"remoteId"`
		// ParentSessionID, when set + the worktree.inherit_permissions
		// setting is on, seeds the new session with the parent's
		// accumulated always-allow permissions (issue #101).
		ParentSessionID string `json:"parentSessionId"`
		// Send, when set, is delivered to the new session before the
		// response returns, saving the client a second round trip on the
		// first message. Failures are reported, never fatal to the launch.
		Send *struct {
			Message string `json:"message"`
			Images  []struct {
				URL  string `json:"url"`
				Mime string `json:"mime"`
			} `json:"images"`
			Model     string `json:"model"`
			Agent     string `json:"agent"`
			Reasoning string `json:"reasoning"`
		} `json:"send"`
	}
	// The first message may carry image attachments.
	if !readAndUnmarshal(w, r, maxSendMessageBody, &req) {
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
	if !req.AutoName && req.Branch == "" {
		http.Error(w, "branch is required", http.StatusBadRequest)
		return
	}
	if !req.AutoName && req.NewBranch && req.BaseRef == "" {
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

	// Resolve the owning host: the explicit owner when the caller named
	// one (preferred, AD-16b), else ForDir inference for
	// backward-compatible local behaviour.
	host, ok := s.resolveOwner(w, req.ProjectDir, req.RemoteID)
	if !ok {
		return
	}
	// Parent permission reads don't depend on the worktree; overlap them.
	inherited := make(chan inheritedRules, 1)
	go func() { inherited <- s.buildInheritedPermissions(r, platformHint(r), req.ParentSessionID) }()
	res, err := host.CreateWorktreeSession(r.Context(), hostsvc.WorktreeSessionRequest{
		ProjectDir: req.ProjectDir,
		Branch:     req.Branch,
		NewBranch:  req.NewBranch,
		BaseRef:    req.BaseRef,
		AutoName:   req.AutoName,
		Prompt:     req.Prompt,
	})
	if err != nil {
		writeWorktreeCreateError(w, err)
		return
	}

	// Seed the new worktree session with the parent's accumulated
	// always-allow permissions (issue #101). Soft-fail: never turn a
	// successful worktree launch into an error; surface any problem via
	// the response fields instead.
	inheritedCount, inheritErr := s.applyInheritedPermissions(r, <-inherited, res.SessionID)

	// Permissions first, then the prompt, so the first turn already runs
	// under the inherited posture.
	platform := opencodePlatformForHost(host)
	sent, sendErr := false, ""
	if req.Send != nil {
		images := make([]platforms.ImageAttachment, 0, len(req.Send.Images))
		for _, img := range req.Send.Images {
			images = append(images, platforms.ImageAttachment{URL: img.URL, Mime: img.Mime})
		}
		if err := s.sendNow(r.Context(), platform, platforms.SendMessageRequest{
			SessionID: res.SessionID, Message: req.Send.Message, Images: images,
			Model: req.Send.Model, Agent: req.Send.Agent, Reasoning: req.Send.Reasoning,
		}); err != nil {
			log.WithError(err).Warn("worktree: sending first message")
			sendErr = err.Error()
		} else {
			sent = true
		}
	}

	writeJSON(w, map[string]interface{}{
		"sessionId":                 res.SessionID,
		"platform":                  platform,
		"firstMessageSent":          sent,
		"firstMessageError":         sendErr,
		"remoteId":                  host.RemoteID(),
		"worktreePath":              res.WorktreePath,
		"branch":                    res.Branch,
		"reused":                    res.Reused,
		"branchExisted":             res.BranchExisted,
		"permissionsInherited":      inheritedCount > 0,
		"permissionsInheritedCount": inheritedCount,
		"permissionsInheritError":   inheritErr,
	})
}
