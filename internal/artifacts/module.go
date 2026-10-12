// Package artifacts publishes immutable files and links for a project:
// the /api/artifacts routes, relay sharing, and the artifacts MCP tool.
// Metadata and blobs live in internal/state.
package artifacts

import (
	"context"
	"net/http"
	"strings"

	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/module"
	"github.com/NoUseFreak/ocman/internal/state"
)

type Deps struct {
	Store *state.DB
	// ResolveProject folds a directory to its local repository root.
	ResolveProject func(ctx context.Context, dir string) (string, error)
	// Projects lists the projects this machine knows.
	Projects func(ctx context.Context) ([]db.ProjectStats, error)
	// Descendants is a session plus every subagent session under it.
	Descendants func(ctx context.Context, sessionID string) ([]string, error)
	Guards      module.Guards
	// FileAuth guards the inert file route, which also accepts a signed URL.
	FileAuth func(http.HandlerFunc) http.HandlerFunc
	// SignFileURL makes a file route absolute, signed when auth is on.
	SignFileURL func(path string) string
	// PublicURL makes an in-app path absolute.
	PublicURL func(path string) string
	// RelayURL is the share relay; empty disables sharing.
	RelayURL  func() string
	Broadcast func(event string, data []byte)
}

type Module struct{ Deps }

var (
	_ module.Module       = (*Module)(nil)
	_ module.ToolProvider = (*Module)(nil)
	_ http.Handler        = (*Module)(nil)
)

func NewModule(deps Deps) *Module { return &Module{deps} }

func (m *Module) Name() string { return "artifacts" }

// Routes mounts one dispatcher; ServeHTTP guards each subroute itself.
func (m *Module) Routes(r module.Router) {
	r.Handle("/api/artifacts", m.ServeHTTP)
	r.Handle("/api/artifacts/", m.ServeHTTP)
}

func (m *Module) Tools() []mcpserver.ServerTool {
	return artifactServerTools(&artifactTools{svc: mcpService{m}})
}

// ServeHTTP routes /api/artifacts, /api/artifacts/stats,
// /api/artifacts/{id}, /api/artifacts/{id}/files/{ordinal} and the
// /api/artifacts/{id}/share(s) relay routes.
func (m *Module) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	auth, local := m.Guards.Auth, m.Guards.Localhost
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/artifacts"), "/"), "/")
	switch {
	case parts[0] == "" && len(parts) == 1:
		get(auth(m.handleList))(w, r)
	case parts[0] == "stats" && len(parts) == 1:
		get(auth(m.handleStats))(w, r)
	case len(parts) == 1 && r.Method == http.MethodDelete:
		local(func(w http.ResponseWriter, r *http.Request) { m.handleDelete(w, r, parts[0]) })(w, r)
	case len(parts) == 1:
		get(auth(func(w http.ResponseWriter, r *http.Request) { m.handleGet(w, r, parts[0]) }))(w, r)
	// Share routes hand out decryption keys, so they are localhost-only.
	case len(parts) == 2 && parts[1] == "share":
		post(local(func(w http.ResponseWriter, r *http.Request) { m.handleShareCreate(w, r, parts[0]) }))(w, r)
	case len(parts) == 2 && parts[1] == "shares":
		get(local(func(w http.ResponseWriter, r *http.Request) { m.handleShareList(w, r, parts[0]) }))(w, r)
	case len(parts) == 3 && parts[1] == "share" && r.Method == http.MethodDelete:
		local(func(w http.ResponseWriter, r *http.Request) { m.handleShareRevoke(w, r, parts[0], parts[2]) })(w, r)
	case len(parts) == 3 && parts[1] == "files":
		get(m.FileAuth(func(w http.ResponseWriter, r *http.Request) { m.serveFile(w, r, parts[0], parts[2], false) }))(w, r)
	// A path segment rather than a query flag, so a preview that navigates
	// itself with "?page=…" (as exported design boards do) stays interactive.
	case len(parts) == 4 && parts[1] == "files" && parts[3] == "interactive":
		get(auth(func(w http.ResponseWriter, r *http.Request) { m.serveFile(w, r, parts[0], parts[2], true) }))(w, r)
	default:
		http.NotFound(w, r)
	}
}

func method(want string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != want {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		h(w, r)
	}
}

func get(h http.HandlerFunc) http.HandlerFunc  { return method(http.MethodGet, h) }
func post(h http.HandlerFunc) http.HandlerFunc { return method(http.MethodPost, h) }
