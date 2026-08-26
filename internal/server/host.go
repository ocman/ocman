package server

import (
	"context"
	"errors"
	"net/http"
	"os/exec"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/forge"
	"github.com/NoUseFreak/ocman/internal/git"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
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
// projects index, refreshing it lazily if it is unloaded or dirty.
func (s *Server) hostProjects(_ context.Context) ([]db.ProjectStats, error) {
	projects, loaded, dirty := s.projectsSnapshotState()
	if !loaded || dirty {
		if err := s.refreshProjectsIndex(); err != nil {
			return nil, err
		}
		projects, _ = s.projectsSnapshot()
	}
	return projects, nil
}

func (s *Server) hostProjectUpstreams(ctx context.Context, dir string) (*hostsvc.ProjectUpstreams, error) {
	repoRoot, err := git.ResolveRepoRoot(ctx, dir) // ocman:allow-host-helper
	if err != nil {
		return nil, err
	}
	var hosts forge.ForgejoHostMap
	if s.integrations != nil && s.integrations.Forgejo != nil {
		hosts = s.integrations.Forgejo
	}
	remotes, err := forge.Detect(ctx, repoRoot, hosts)
	return &hostsvc.ProjectUpstreams{RepoRoot: repoRoot, Remotes: remotes}, err
}

func (s *Server) hostFetchPRHead(ctx context.Context, req hostsvc.FetchPRHeadRequest) (string, error) {
	upstreams, err := s.hostProjectUpstreams(ctx, req.RepoRoot)
	if err != nil {
		return "", err
	}
	rem, ok := findRemote(upstreams.Remotes, req.Remote)
	if !ok {
		return "", errors.New("remote not found among project upstreams")
	}
	f, ok := s.resolveForge(rem)
	if !ok {
		return "", errors.New("no forge client configured for " + rem.Host)
	}
	ctx, cancel := context.WithTimeout(ctx, prHeadFetchTimeout)
	defer cancel()
	return f.FetchPRHead(ctx, upstreams.RepoRoot, req.Remote, req.Number)
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
// (#268 multi-remote requirement, same as CreateWorktreeSession).
//
// dir is folded to the project root first (#532): a parent session
// living in a managed worktree must reuse the main checkout's single
// instance, not launch a second one keyed on the worktree's toplevel.
func (s *Server) ensureProjectOpencodePort(ctx context.Context, dir string) (string, error) {
	dir = projectRootForDirectory(dir)
	res, err := s.router().ForDir(dir).EnsureProjectOpencode(ctx, hostsvc.EnsureProjectOpencodeRequest{ProjectDir: dir})
	if err != nil {
		return "", err
	}
	return res.Port(), nil
}
