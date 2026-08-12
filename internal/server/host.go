package server

import (
	"context"
	"fmt"
	"net/http"
	"os/exec"
	"strings"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/git"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	internalmcp "github.com/NoUseFreak/ocman/internal/mcp"
	"github.com/NoUseFreak/ocman/internal/tmux"
	"github.com/NoUseFreak/ocman/internal/whisper"
)

// host.go wires the server-package host operations (tmux, projects,
// capabilities) into the local hostsvc.Host. These are the dependency
// shims the local Host calls; the the git package call sites live in
// internal/hostsvc/local directly. See AD-16.

// router returns the host router, lazily building a local-only one when
// the Server was constructed directly (e.g. some tests use &Server{}
// rather than New). Handlers must resolve host operations through this.
func (s *Server) router() *hostsvc.Router {
	// Once, not a bare nil check: StartOnListener launches background
	// loops (auto-archive, prompt schedules, ...) that reach router()
	// concurrently with the first HTTP handlers, and an unguarded lazy
	// assignment races them. The nil check stays *inside* the Once so a
	// test that installs its own router before first use still wins.
	s.hostRouterOnce.Do(func() {
		if s.hostRouter == nil {
			s.hostRouter = hostsvc.NewRouter(s.newLocalHost())
		}
	})
	return s.hostRouter
}

// resolveOwner resolves the Host that owns an action. When the client
// named an owner explicitly it must resolve to a *registered* one: a
// stale, disconnected or mistyped remote ID must never degrade to the
// hub, which would run the action (worktree creation, process launch,
// live shell) on the wrong machine. It writes the rejection and returns
// false in that case. An empty remoteID falls back to directory
// inference; ""/"local" resolve to this machine.
//
// Status choice — 503, deliberately *not* 409. Two of the routes that
// call this already use 409 for a genuine domain conflict:
// handleWorktreeRemove (ErrMainWorktree / ErrWorktreeDirty) and
// handleWorktreeCreateAndLaunch (ErrBranchCheckedOutElsewhere /
// ErrPathConflict), and the frontend distinguishes the dirty-worktree
// case by matching the response *prose* so it can offer "Force delete".
// Overloading 409 would make the status two-valued, separated only by
// body text that any wording change silently breaks. 503 is also the
// honest semantic: the machine that owns this work is unreachable right
// now, and the request may succeed once it reconnects. The structured
// envelope follows the `requires_fetch` precedent in
// handlers_project_handle.go so clients can branch on a code, not prose.
func (s *Server) resolveOwner(w http.ResponseWriter, dir, remoteID string) (hostsvc.Host, bool) {
	if remoteID == "" {
		return s.router().ForDir(dir), true
	}
	host, ok := s.router().LookupRemote(remoteID)
	if !ok {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{
			"error": map[string]string{
				"code":     "remote_not_connected",
				"remoteId": remoteID,
				"message":  "remote " + remoteID + " is not connected",
			},
		})
		return nil, false
	}
	return host, true
}

// hostTmuxSessions adapts listTmuxSessions to the hostsvc shape.
func (s *Server) hostTmuxSessions() ([]hostsvc.TmuxSession, error) {
	sessions, err := tmux.ListSessions()
	if err != nil {
		return nil, err
	}
	out := make([]hostsvc.TmuxSession, 0, len(sessions))
	for _, ts := range sessions {
		out = append(out, hostsvc.TmuxSession{Name: ts.Name, Path: ts.ResolvedPath})
	}
	return out, nil
}

// hostProjects returns this host's known projects from the in-memory
// projects index, refreshing it lazily if it hasn't loaded yet.
func (s *Server) hostProjects(_ context.Context) ([]db.ProjectStats, error) {
	projects, loaded := s.projectsSnapshot()
	if !loaded {
		if err := s.refreshProjectsIndex(); err != nil {
			return nil, err
		}
		projects, _ = s.projectsSnapshot()
	}
	return projects, nil
}

// hostCaps reports which host operations are available on this machine.
// git/tmux/opencode-on-PATH gate worktrees + tmux; whisper gates voice.
func (s *Server) hostCaps() hostsvc.HostCaps {
	tmuxOK := tmux.IsAvailable()
	gitOK := lookPathOK("git")
	opencodeOK := lookPathOK("opencode")
	return hostsvc.HostCaps{
		GitDiff:   gitOK,
		Worktrees: tmuxOK && gitOK && opencodeOK,
		Tmux:      tmuxOK,
		Projects:  s.db != nil,
		Whisper:   whisper.Available(),
		// Launch availability is opencode-on-PATH plus the native runtime's
		// prerequisite (tmux) today; a future container runtime drops the
		// tmux dependency. Gated separately from Tmux (#390 / AD-8).
		OpencodeLaunch: opencodeOK && tmuxOK,
	}
}

func lookPathOK(bin string) bool {
	_, err := exec.LookPath(bin)
	return err == nil
}

// ensureProjectOpencodePort guarantees the project's single opencode
// instance is running for dir and returns its port, routed through the
// owning host (ForDir) so a remote's project never launches on the hub
// (#268 multi-remote requirement, same as CreateWorktreeSession). It is
// the mcp.ProjectOpencodeEnsurer the SessionLauncher uses.
func (s *Server) ensureProjectOpencodePort(ctx context.Context, dir string) (string, error) {
	res, err := s.router().ForDir(dir).EnsureProjectOpencode(ctx, hostsvc.EnsureProjectOpencodeRequest{ProjectDir: dir})
	if err != nil {
		return "", err
	}
	return res.Port(), nil
}

// hostWorktreeSession creates a worktree and its session on the host that
// owns the project directory. It is the mcp.WorktreeSessionCreator the
// SessionLauncher uses, so an MCP split for a remote-owned project
// creates the worktree on that machine instead of on the hub (AD-16).
func (s *Server) hostWorktreeSession(ctx context.Context, req internalmcp.WorktreeSessionRequest) (*internalmcp.WorktreeSessionResult, error) {
	res, err := s.router().ForDir(req.ParentDir).CreateWorktreeSession(ctx, hostsvc.WorktreeSessionRequest{
		ProjectDir: req.ParentDir,
		Branch:     req.Branch,
		NewBranch:  req.NewBranch,
		BaseRef:    req.BaseRef,
	})
	if err != nil {
		return nil, err
	}
	return &internalmcp.WorktreeSessionResult{
		SessionID:    res.SessionID,
		WorktreePath: res.WorktreePath,
		Branch:       res.Branch,
	}, nil
}

// hostGitContext reads the branch + uncommitted-changes summary for dir
// from the host that owns it, for MCP prompt enrichment. Both halves are
// soft: a host that can't answer just yields less context, never an
// error — but it is always *that* host, never the hub's copy of the path.
func (s *Server) hostGitContext(ctx context.Context, dir string) (internalmcp.GitContext, error) {
	host := s.router().ForDir(dir)
	var out internalmcp.GitContext
	if info, err := host.GitInfo(ctx, []string{dir}); err == nil {
		out.Branch = info[dir].Branch
	}
	if diff, err := host.GitDiff(ctx, dir, hostsvc.GitDiffOptions{}); err == nil && diff != nil {
		out.DiffStat = formatDiffStat(diff.Files)
	}
	return out, nil
}

// formatDiffStat renders a `git diff --stat`-shaped summary from the
// host's structured diff, so the prompt keeps its familiar shape without
// a second (shell) way to ask for the same data.
func formatDiffStat(files []git.DiffFile) string {
	if len(files) == 0 {
		return ""
	}
	var b strings.Builder
	var additions, deletions int
	for _, f := range files {
		fmt.Fprintf(&b, " %s | %d +, %d -\n", f.Path, f.Additions, f.Deletions)
		additions += f.Additions
		deletions += f.Deletions
	}
	fmt.Fprintf(&b, " %d file(s) changed, %d insertion(s)(+), %d deletion(s)(-)",
		len(files), additions, deletions)
	return b.String()
}

// killHostTmuxTarget kills a legacy child session's tmux target on the
// host that owns its worktree. hostsvc.Host has no kill-target method, so
// a remote owner fails closed with a clear error instead of killing a
// same-named pane on the hub; cancel_session treats that as a soft
// failure and still marks the child cancelled.
func (s *Server) killHostTmuxTarget(_ context.Context, dir, target string) error {
	if host := s.router().ForDir(dir); host.RemoteID() != "" && host.RemoteID() != "local" {
		return fmt.Errorf("killing a tmux target is not supported for remote-owned sessions (owner %s)", host.RemoteID())
	}
	return tmux.KillTarget(target) // ocman:allow-host-helper
}
