package server

import (
	"net/http"
	"testing"

	"github.com/ocman/ocman/internal/module"
)

// serveModule sends req through m's routes, mounted with the server's real
// guards, as a loopback, origin-less client.
func serveModule(s *Server, m module.Module, w http.ResponseWriter, req *http.Request) {
	mux := http.NewServeMux()
	m.Routes(moduleRouter{s: s, mux: mux})
	req.RemoteAddr = "127.0.0.1:12345"
	mux.ServeHTTP(w, req)
}

// serveRoutes serves req through the server's full route table.
func serveRoutes(t *testing.T, srv *Server, w http.ResponseWriter, req *http.Request) {
	t.Helper()
	mustRoutes(t, srv).ServeHTTP(w, req)
}

// mustRoutes builds the server's full route table, failing the test on error.
func mustRoutes(t *testing.T, srv *Server) http.Handler {
	t.Helper()
	mux, err := srv.routes()
	if err != nil {
		t.Fatal(err)
	}
	return mux
}
