// Package module is the contract between the server and a feature module.
//
// A module owns its routes, background work and MCP tools; the server only
// iterates the registered modules. Optional capabilities are separate
// interfaces, checked with a type assertion, so a module implements only
// what it needs. Modules never import internal/server (the compiler refuses
// the cycle), and reach shared services through what they were built with.
package module

import (
	"context"
	"net/http"

	mcpserver "github.com/mark3labs/mcp-go/server"
)

// Module is a feature the server mounts.
type Module interface {
	Name() string
	Routes(Router)
}

// Router registers a module's HTTP routes behind the server's guards.
type Router interface {
	// Handle registers a route without a guard; the module guards each
	// subroute itself with Guards.
	Handle(pattern string, h http.HandlerFunc)
	// Local registers a localhost-only route (peer and browser origin).
	Local(pattern string, h http.HandlerFunc)
	// Setting registers a Settings route without a guard: a successful POST
	// broadcasts that settings changed so other clients reload them.
	Setting(pattern string, h http.HandlerFunc)
	// LocalSetting is Local for a Settings route: a successful POST
	// broadcasts that settings changed so other clients reload them.
	LocalSetting(pattern string, h http.HandlerFunc)
}

// Guards are the server's auth middleware, for a module that guards
// subroutes of one mount differently.
type Guards struct {
	// Auth requires a valid login when password auth is on.
	Auth func(http.HandlerFunc) http.HandlerFunc
	// Localhost allows only a loopback peer and browser origin.
	Localhost func(http.HandlerFunc) http.HandlerFunc
}

// Starter runs once, synchronously, before the server accepts requests.
// ctx is cancelled when the server stops; a module whose initialization must
// outlive that detaches it with context.WithoutCancel. An error aborts
// startup.
type Starter interface {
	Start(ctx context.Context) error
}

// Runner is background work for the server's lifetime. Run returns when
// ctx is cancelled; the server waits for it before closing databases.
type Runner interface {
	Run(ctx context.Context)
}

// Launcher starts work whose agents call back into the server: Launch runs
// once the MCP listener is up, before the HTTP server serves. An error
// aborts startup.
type Launcher interface {
	Launch(ctx context.Context) error
}

// Closer stops a launched module after the HTTP and MCP listeners have shut
// down, so no in-flight request reaches it mid-close, and before workers
// are joined and databases closed.
type Closer interface {
	Close()
}

// Finalizer releases resources opened during Start after handlers are fenced
// and all workers joined, including failed startup before Run or Launch. It
// must tolerate unopened resources and repeated calls.
type Finalizer interface {
	Finalize()
}

// ToolProvider contributes MCP tools to the server's MCP endpoint.
type ToolProvider interface {
	Tools() []mcpserver.ServerTool
}
