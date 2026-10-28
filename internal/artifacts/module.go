// Package artifacts publishes immutable files and links for a project:
// the /api/artifacts routes, relay sharing, and the artifacts MCP tool.
// Metadata and blobs live in internal/state.
package artifacts

import (
	"context"
	"net/http"

	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/module"
	"github.com/ocman/ocman/internal/state"
)

type Deps struct {
	Store *state.DB
	// ResolveProject folds a directory to its local repository root.
	ResolveProject func(ctx context.Context, dir string) (string, error)
	// Projects lists the projects this machine knows.
	Projects func(ctx context.Context) ([]db.ProjectStats, error)
	// Descendants is a session plus every subagent session under it.
	Descendants func(ctx context.Context, sessionID string) ([]string, error)
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

type Module struct {
	Deps
}

var (
	_ module.Module       = (*Module)(nil)
	_ module.ToolProvider = (*Module)(nil)
)

func NewModule(deps Deps) *Module { return &Module{Deps: deps} }

func (m *Module) Name() string { return "artifacts" }

// Routes registers each subroute with its method and access guard.
func (m *Module) Routes(r module.Router) {
	auth, local := r.Guards().Auth, r.Guards().Localhost
	r.Handle("GET /api/artifacts", m.handleList)
	r.Handle("GET /api/artifacts/{$}", m.handleList)
	// Reserve "stats" for all methods so DELETE cannot treat it as an ID.
	stats := http.NewServeMux()
	stats.HandleFunc("GET /api/artifacts/stats", auth(m.handleStats))
	r.Public("/api/artifacts/stats", stats.ServeHTTP)
	item := http.NewServeMux()
	item.HandleFunc("DELETE /api/artifacts/{id}", local(func(w http.ResponseWriter, r *http.Request) {
		m.handleDelete(w, r, r.PathValue("id"))
	}))
	item.HandleFunc("GET /api/artifacts/{id}", auth(func(w http.ResponseWriter, r *http.Request) {
		m.handleGet(w, r, r.PathValue("id"))
	}))
	r.Public("/api/artifacts/{id}", item.ServeHTTP)
	// Share routes hand out decryption keys, so they are localhost-only.
	r.Local("POST /api/artifacts/{id}/share", func(w http.ResponseWriter, r *http.Request) {
		m.handleShareCreate(w, r, r.PathValue("id"))
	})
	r.Local("GET /api/artifacts/{id}/shares", func(w http.ResponseWriter, r *http.Request) {
		m.handleShareList(w, r, r.PathValue("id"))
	})
	r.Local("DELETE /api/artifacts/{id}/share/{share}", func(w http.ResponseWriter, r *http.Request) {
		m.handleShareRevoke(w, r, r.PathValue("id"), r.PathValue("share"))
	})
	r.Public("GET /api/artifacts/{id}/files/{ordinal}", m.FileAuth(func(w http.ResponseWriter, r *http.Request) {
		m.serveFile(w, r, r.PathValue("id"), r.PathValue("ordinal"), false)
	}))
	// A path segment rather than a query flag, so a preview that navigates
	// itself with "?page=…" (as exported design boards do) stays interactive.
	r.Handle("GET /api/artifacts/{id}/files/{ordinal}/interactive", func(w http.ResponseWriter, r *http.Request) {
		m.serveFile(w, r, r.PathValue("id"), r.PathValue("ordinal"), true)
	})
}

func (m *Module) Tools() []mcpserver.ServerTool {
	return artifactServerTools(&artifactTools{svc: mcpService{m}})
}
