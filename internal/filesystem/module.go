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
	"time"

	"github.com/ocman/ocman/internal/git"
	"github.com/ocman/ocman/internal/module"
	"github.com/ocman/ocman/internal/state"
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
	Owner             func(w http.ResponseWriter, r *http.Request) (dir string, files RepoFiles, ok bool)
	PublishedResource http.HandlerFunc
}

type Module struct {
	Deps
	keyMu sync.Mutex
	key   []byte
	// now is the clock file-URL expiries are minted and checked against;
	// tests replace it.
	now func() time.Time
}

var _ module.Module = (*Module)(nil)

func NewModule(deps Deps) *Module { return &Module{Deps: deps, now: time.Now} }

func (m *Module) Name() string { return "filesystem" }

// Routes: the directory picker walks the hub's disk, so it is
// localhost-only; signed files and repository reads need a login.
func (m *Module) Routes(r module.Router) {
	if m.PublishedResource != nil {
		r.Handle("/api/remote-resources/{remoteId}/{resource...}", m.PublishedResource)
	}
	r.Local("GET /api/filesystem/directories", m.handleDirectories)
	r.Local("GET /api/filesystem/directory-search", m.handleDirectorySearch)
	r.Handle("GET "+filePathPrefix, m.handleFileProxy)
	r.Handle("POST "+fileRefreshPath, m.handleFileURLRefresh)
	r.Handle("GET /api/git/files", m.handleRepoFiles)
	r.Handle("GET /api/git/file", m.handleRepoFile)
}

// ServePublishedResource is called only after the authenticated remote transport
// has checked its closed route allowlist.
func (m *Module) ServePublishedResource(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == fileRefreshPath {
		m.handleFileURLRefresh(w, r)
	} else {
		m.handleFileProxy(w, r)
	}
}
