package server

import "net/http"

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
