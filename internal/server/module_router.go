package server

import (
	"net/http"
	"strings"

	"github.com/NoUseFreak/ocman/internal/module"
)

// moduleRouter mounts module routes behind the server's guards and applies
// ownership checks before session-scoped handlers inspect paths.
type moduleRouter struct {
	s   *Server
	mux *http.ServeMux
}

func (r moduleRouter) Handle(pattern string, h http.HandlerFunc) {
	r.mux.HandleFunc(pattern, r.sessionPath(pattern, r.s.requireAuth(h)))
}

func (r moduleRouter) Public(pattern string, h http.HandlerFunc) {
	r.mux.HandleFunc(pattern, r.sessionPath(pattern, h))
}

func (r moduleRouter) Local(pattern string, h http.HandlerFunc) {
	r.mux.HandleFunc(pattern, r.sessionPath(pattern, r.s.requireLocalhost(h)))
}

func (r moduleRouter) Setting(pattern string, h http.HandlerFunc) {
	r.mux.HandleFunc(pattern, r.sessionPath(pattern, r.s.settingsHandler(r.s.requireAuth(h))))
}

func (r moduleRouter) LocalSetting(pattern string, h http.HandlerFunc) {
	r.mux.HandleFunc(pattern, r.sessionPath(pattern, r.s.settingsHandler(r.s.requireLocalhost(h))))
}

func (r moduleRouter) Guards() module.Guards {
	return module.Guards{Auth: r.s.requireAuth, Localhost: r.s.requireLocalhost}
}

func (r moduleRouter) sessionPath(pattern string, h http.HandlerFunc) http.HandlerFunc {
	// Module commands such as /api/session/snooze take their session ID in
	// the body. Only a declared path parameter is an identity to resolve.
	if strings.Contains(pattern, "/api/session/{") {
		if !strings.Contains(pattern, "/api/session/{id}") {
			panic("session route must name its identity {id}: " + pattern)
		}
		return r.s.withOwnedSessionPath(h)
	}
	return h
}
