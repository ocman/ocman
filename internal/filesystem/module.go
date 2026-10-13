// Package filesystem serves read-only browsing of files and directories:
// the hub's directory picker (browse + fuzzy search), the signed proxy for
// files an agent embedded with the MCP embed_file tool, and the
// owner-routed repository file explorer. It writes nothing but its own
// signing key.
package filesystem

import (
	"context"
	"net/http"
	"sync"

	"github.com/NoUseFreak/ocman/internal/git"
	"github.com/NoUseFreak/ocman/internal/module"
	"github.com/NoUseFreak/ocman/internal/state"
)

// RepoFiles reads the listed files of the repository containing dir on the
// machine that owns it.
type RepoFiles interface {
	ListRepoFiles(ctx context.Context, dir string, ignored bool) (*git.FileList, error)
	ReadRepoFile(ctx context.Context, dir, path string, ignored bool) (*git.FileContent, error)
}

type Deps struct {
	// Store persists the embedded-file signing key; without it the signed
	// file routes fail.
	Store *state.DB
	// Owner parses the required absolute ?dir= and ?remoteId= and resolves
	// the owning machine, writing the error response itself (400, or 503
	// for an explicit remote that is not connected).
	Owner  func(w http.ResponseWriter, r *http.Request) (dir string, files RepoFiles, ok bool)
	Guards module.Guards
}

type Module struct {
	Deps
	keyMu sync.Mutex
	key   []byte
}

var _ module.Module = (*Module)(nil)

func NewModule(deps Deps) *Module { return &Module{Deps: deps} }

func (m *Module) Name() string { return "filesystem" }

// Routes: the directory picker walks the hub's disk, so it is
// localhost-only; signed files and repository reads need a login.
func (m *Module) Routes(r module.Router) {
	r.Local("/api/filesystem/directories", getOnly(m.handleDirectories))
	r.Local("/api/filesystem/directory-search", getOnly(m.handleDirectorySearch))
	auth := m.Guards.Auth
	r.Handle(filePathPrefix, getOnly(auth(m.handleFileProxy)))
	r.Handle("/api/git/files", getOnly(auth(m.handleRepoFiles)))
	r.Handle("/api/git/file", getOnly(auth(m.handleRepoFile)))
}

// getOnly answers 405 itself: a "GET " mux pattern would hand other
// methods to the SPA fallback as 404.
func getOnly(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		h(w, r)
	}
}
