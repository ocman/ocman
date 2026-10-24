package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/module/moduletest"
)

// serveGit serves r through the git module wired to this server, without
// guards, as the composition tests called the former server handlers.
// Guard placement is covered in gitmod and TestGitModuleRouteGuards.
func (s *Server) serveGit(w http.ResponseWriter, r *http.Request) {
	moduletest.Mount(s.gitModule()).ServeHTTP(w, r)
}

func (s *Server) handleGitInfo(w http.ResponseWriter, r *http.Request)     { s.serveGit(w, r) }
func (s *Server) handleGitBranches(w http.ResponseWriter, r *http.Request) { s.serveGit(w, r) }
func (s *Server) handleGitCheckout(w http.ResponseWriter, r *http.Request) { s.serveGit(w, r) }
func (s *Server) handleGitDiff(w http.ResponseWriter, r *http.Request)     { s.serveGit(w, r) }
func (s *Server) handleWorktreeList(w http.ResponseWriter, r *http.Request) {
	s.serveGit(w, r)
}
func (s *Server) handleWorktreeDefaultBaseRef(w http.ResponseWriter, r *http.Request) {
	s.serveGit(w, r)
}
func (s *Server) handleWorktreeRemove(w http.ResponseWriter, r *http.Request) { s.serveGit(w, r) }
func (s *Server) handleWorktreeCreateAndLaunch(w http.ResponseWriter, r *http.Request) {
	s.serveGit(w, r)
}

// The git routes are mounted from the module registry: reads stay open to
// any authenticated client, mutations and launches are localhost-only, and
// the method check runs ahead of the localhost guard.
func TestGitModuleRouteGuards(t *testing.T) {
	mux, err := testServer(t).routes()
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/api/git/info?dirs=/r", http.StatusBadRequest}, // mounted, remoteId required
		{http.MethodGet, "/api/worktree/list", http.StatusBadRequest},    // mounted, dir required
		{http.MethodGet, "/api/git/checkout", http.StatusMethodNotAllowed},
		{http.MethodGet, "/api/worktree/remove", http.StatusMethodNotAllowed},
		{http.MethodPost, "/api/git/diff", http.StatusMethodNotAllowed},
		{http.MethodPost, "/api/git/checkout", http.StatusForbidden},
		{http.MethodPost, "/api/worktree/remove", http.StatusForbidden},
		{http.MethodPost, "/api/worktree/create-and-launch", http.StatusForbidden},
	} {
		req := httptest.NewRequest(tt.method, tt.path, strings.NewReader(`{}`))
		req.RemoteAddr = "192.0.2.1:1"
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		if rr.Code != tt.want {
			t.Errorf("%s %s = %d, want %d", tt.method, tt.path, rr.Code, tt.want)
		}
	}
}
