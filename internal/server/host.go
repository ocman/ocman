package server

import (
	"context"
	"os/exec"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/whisper"
)

// host.go wires the server-package host operations (tmux, projects,
// capabilities) into the local hostsvc.Host. These are the dependency
// shims the local Host calls; the gitinfo/worktree call sites live in
// internal/hostsvc/local directly. See AD-16.

// router returns the host router, lazily building a local-only one when
// the Server was constructed directly (e.g. some tests use &Server{}
// rather than New). Handlers must resolve host operations through this.
func (s *Server) router() *hostsvc.Router {
	if s.hostRouter == nil {
		s.hostRouter = hostsvc.NewRouter(s.newLocalHost())
	}
	return s.hostRouter
}

// hostTmuxSessions adapts listTmuxSessions to the hostsvc shape.
func (s *Server) hostTmuxSessions() ([]hostsvc.TmuxSession, error) {
	sessions, err := listTmuxSessions()
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
	tmuxOK := isTmuxAvailable()
	gitOK := lookPathOK("git")
	opencodeOK := lookPathOK("opencode")
	return hostsvc.HostCaps{
		GitDiff:   gitOK,
		Worktrees: tmuxOK && gitOK && opencodeOK,
		Tmux:      tmuxOK,
		Projects:  s.db != nil,
		Whisper:   whisper.Available(),
	}
}

func lookPathOK(bin string) bool {
	_, err := exec.LookPath(bin)
	return err == nil
}
