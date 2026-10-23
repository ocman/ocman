package server

import "net/http"

// mountSessionRoutes registers the core /api/session/... endpoints. Feature
// modules mount their own session patterns (lifecycle actions, queue,
// shares, auto-approve) on the same mux.
func (s *Server) mountSessionRoutes(mux *http.ServeMux) {
	for _, route := range []struct {
		pattern string
		handler http.HandlerFunc
	}{
		// Non-session reserved sub-paths (no {id}).
		{"POST /api/session/archive", s.handleArchiveSession},
		{"POST /api/session/seen", s.handleSeenSession},
		{"POST /api/session/pin", s.handlePinSession},

		{"GET /api/session/{id}", s.handleSession},
		{"GET /api/session/{id}/agents", s.handleSessionAgents},
		{"GET /api/session/{id}/commands", s.handleSessionCommands},
		{"GET /api/session/{id}/models", s.handleSessionModels},
		{"GET /api/session/{id}/changes", s.handleSessionChanges},
		{"GET /api/session/{id}/info", s.handleSessionInfo},
		{"GET /api/session/{id}/permissions", s.handleSessionPermissions},
		{"GET /api/session/{id}/questions", s.handleSessionQuestions},
		{"GET /api/session/{id}/tasks", s.handleSessionTasks},

		{"POST /api/session/{id}/questions/{qid}", s.handleSessionQuestion},
		{"POST /api/session/{id}/questions/{qid}/reject", s.handleSessionQuestion},
		{"POST /api/session/{id}/permissions/{pid}", s.handleSessionPermission},
		{"POST /api/session/{id}/message", s.handleSessionMessage},
	} {
		mux.HandleFunc(route.pattern, s.withOwnedSessionPath(s.requireAuth(route.handler)))
	}
}
