package server

import "net/http"

// moduleRouter mounts module routes behind the server's guards and applies
// public identity translation before session-scoped handlers inspect paths.
type moduleRouter struct {
	s   *Server
	mux *http.ServeMux
}

func (r moduleRouter) Handle(pattern string, h http.HandlerFunc) {
	r.mux.HandleFunc(pattern, r.s.withOwnedSessionPath(h))
}

func (r moduleRouter) Local(pattern string, h http.HandlerFunc) {
	r.mux.HandleFunc(pattern, r.s.withOwnedSessionPath(r.s.requireLocalhost(h)))
}

func (r moduleRouter) Setting(pattern string, h http.HandlerFunc) {
	r.mux.HandleFunc(pattern, r.s.withOwnedSessionPath(r.s.settingsHandler(r.s.withSessionViews(h))))
}

func (r moduleRouter) LocalSetting(pattern string, h http.HandlerFunc) {
	r.mux.HandleFunc(pattern, r.s.withOwnedSessionPath(r.s.settingsHandler(r.s.requireLocalhost(h))))
}
