package server

import (
	"context"
	"net/http"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/sessionlifecycle"
)

func (s *Server) handleSessionCommand(w http.ResponseWriter, r *http.Request) {
	s.sessionLifecycleModule().HandleSessionCommand(w, r)
}

func (s *Server) handleSessionShell(w http.ResponseWriter, r *http.Request) {
	s.sessionLifecycleModule().HandleSessionShell(w, r)
}

func (s *Server) handleSessionRename(w http.ResponseWriter, r *http.Request) {
	s.sessionLifecycleModule().HandleSessionRename(w, r)
}

func (s *Server) handleSessionAbort(w http.ResponseWriter, r *http.Request) {
	s.sessionLifecycleModule().HandleSessionAbort(w, r)
}

func (s *Server) handleSessionRevert(w http.ResponseWriter, r *http.Request) {
	s.sessionLifecycleModule().HandleSessionRevert(w, r)
}

func (s *Server) handleSessionUnrevert(w http.ResponseWriter, r *http.Request) {
	s.sessionLifecycleModule().HandleSessionUnrevert(w, r)
}

func (s *Server) handleSessionCompact(w http.ResponseWriter, r *http.Request) {
	s.sessionLifecycleModule().HandleSessionCompact(w, r)
}

func (s *Server) handleSessionFork(w http.ResponseWriter, r *http.Request) {
	s.sessionLifecycleModule().HandleSessionFork(w, r)
}

func (s *Server) handleSessionMove(w http.ResponseWriter, r *http.Request) {
	s.sessionLifecycleModule().HandleSessionMove(w, r)
}

func (s *Server) handleSessionRestartOpencode(w http.ResponseWriter, r *http.Request) {
	s.sessionLifecycleModule().HandleSessionRestartOpencode(w, r)
}

func (s *Server) handleSessionReloadOpencode(w http.ResponseWriter, r *http.Request) {
	s.sessionLifecycleModule().HandleSessionReloadOpencode(w, r)
}

func (s *Server) handleSessionAttachment(w http.ResponseWriter, r *http.Request) {
	s.sessionLifecycleModule().HandleSessionAttachment(w, r)
}

func (s *Server) handleSessionPermissionRulesGet(w http.ResponseWriter, r *http.Request) {
	s.sessionLifecycleModule().HandleSessionPermissionRulesGet(w, r)
}

func (s *Server) handleSessionPermissionRulesSet(w http.ResponseWriter, r *http.Request) {
	s.sessionLifecycleModule().HandleSessionPermissionRulesSet(w, r)
}

type restartTarget struct {
	host    hostsvc.Host
	root    string
	machine bool
}

func (s *Server) restartTargets(ctx context.Context, dir, remoteID string, all bool) ([]restartTarget, error) {
	targets, err := s.sessionLifecycleModule().RestartTargets(ctx, dir, remoteID, all)
	if err != nil {
		return nil, err
	}
	var out []restartTarget
	for _, target := range targets {
		out = append(out, restartTarget{host: target.Host, root: target.Root, machine: target.Machine})
	}
	return out, nil
}

func sameProject(root, dir string) bool { return sessionlifecycle.SameProject(root, dir) }
