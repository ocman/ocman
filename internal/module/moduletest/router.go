// Package moduletest mounts feature modules in tests the way the server does,
// with substitutable guards.
package moduletest

import (
	"net/http"

	"github.com/ocman/ocman/internal/module"
)

// Router is a module.Router over Mux. Handle and Setting apply Auth, Local
// and LocalSetting apply Localhost, Public applies neither; a nil guard
// passes requests through.
type Router struct {
	Mux       *http.ServeMux
	Auth      func(http.HandlerFunc) http.HandlerFunc
	Localhost func(http.HandlerFunc) http.HandlerFunc
}

var _ module.Router = Router{}

// Mount registers m's routes on a new mux with pass-through guards.
func Mount(m module.Module) *http.ServeMux {
	r := Router{Mux: http.NewServeMux()}
	m.Routes(r)
	return r.Mux
}

func (r Router) Handle(p string, h http.HandlerFunc)       { r.Mux.HandleFunc(p, r.Guards().Auth(h)) }
func (r Router) Public(p string, h http.HandlerFunc)       { r.Mux.HandleFunc(p, h) }
func (r Router) Local(p string, h http.HandlerFunc)        { r.Mux.HandleFunc(p, r.Guards().Localhost(h)) }
func (r Router) Setting(p string, h http.HandlerFunc)      { r.Handle(p, h) }
func (r Router) LocalSetting(p string, h http.HandlerFunc) { r.Local(p, h) }

func (r Router) Guards() module.Guards {
	g := module.Guards{Auth: r.Auth, Localhost: r.Localhost}
	if g.Auth == nil {
		g.Auth = pass
	}
	if g.Localhost == nil {
		g.Localhost = pass
	}
	return g
}

func pass(h http.HandlerFunc) http.HandlerFunc { return h }
