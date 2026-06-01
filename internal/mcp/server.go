package mcp

import (
	"net/http"

	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/worktree"
)

// Deps holds the dependencies injected into the MCP server by the
// ocman server package. All fields are required unless noted.
type Deps struct {
	// OcDB is the read-only OpenCode database handle. May be nil when
	// the OpenCode platform adapter is not registered.
	OcDB *db.DB

	// StateDB is the writable ocman state database.
	StateDB *state.DB

	// Registry is the platform adapter registry. Used to resolve the
	// OpenCode adapter for CreateSession / SendMessage calls.
	Registry *platforms.Registry

	// PlatformID is the platform identifier to use for child sessions
	// (e.g. "opencode").
	PlatformID string

	// CreateWorktree is the worktree creation function. Defaults to
	// worktree.Create when nil.
	CreateWorktree WorktreeCreator

	// LaunchTmux is the tmux launcher function. Must be set by the
	// caller (the server package injects launchOpencodeInProjectTmuxWindow).
	LaunchTmux TmuxLauncher

	// DiscoverPort is the port discovery function. Must be set by the
	// caller (the server package injects the OpenCode port discoverer).
	DiscoverPort PortDiscoverer
}

// Server wraps the mcp-go MCPServer and exposes an http.Handler.
type Server struct {
	handler http.Handler
}

// New constructs a Server from the given dependencies, registers all
// MCP tools, and wraps the result in a StreamableHTTPServer.
func New(deps Deps) *Server {
	// Apply defaults for injectable functions.
	if deps.CreateWorktree == nil {
		deps.CreateWorktree = worktree.Create
	}

	// Resolve the platform adapter.
	var adapter platformAdapter
	if deps.Registry != nil {
		for _, p := range deps.Registry.Platforms() {
			if string(p.ID()) == deps.PlatformID {
				adapter = p
				break
			}
		}
	}

	// Build the prompt composer.
	var composer *PromptComposer
	if deps.OcDB != nil {
		composer = NewPromptComposer(deps.OcDB)
	} else {
		// Nil DB: composer will return minimal prompts (intent only).
		composer = NewPromptComposer(&nullSessionReader{})
	}

	// Build the session launcher.
	launcher := NewSessionLauncher(
		deps.StateDB,
		adapter,
		deps.CreateWorktree,
		deps.LaunchTmux,
		deps.DiscoverPort,
	)

	// Build the mcp-go server.
	s := mcpserver.NewMCPServer(
		"ocman",
		"1.0.0",
		mcpserver.WithToolCapabilities(false),
	)

	// Register split tools.
	split := &splitTools{
		composer: composer,
		launcher: launcher,
		platform: deps.PlatformID,
	}
	addSplitTools(s, split)

	// Register status tools.
	var ocDB statusSessionReader
	if deps.OcDB != nil {
		ocDB = deps.OcDB
	}
	status := &statusTools{
		stateDB: deps.StateDB,
		ocDB:    ocDB,
	}
	addStatusTools(s, status)

	// Wrap in a StreamableHTTPServer (implements http.Handler).
	httpHandler := mcpserver.NewStreamableHTTPServer(s,
		mcpserver.WithStateLess(true),
	)

	return &Server{handler: httpHandler}
}

// Handler returns the http.Handler for the MCP server. Mount this on
// the ocman HTTP mux at /mcp (with requireLocalhost).
func (s *Server) Handler() http.Handler {
	return s.handler
}

// ServerTools builds the list of server.ServerTool entries for the given
// dependencies. Used by tests that want to register tools into an mcptest
// server without going through the full HTTP transport.
func ServerTools(deps Deps) []mcpserver.ServerTool {
	if deps.CreateWorktree == nil {
		deps.CreateWorktree = worktree.Create
	}

	var adapter platformAdapter
	if deps.Registry != nil {
		for _, p := range deps.Registry.Platforms() {
			if string(p.ID()) == deps.PlatformID {
				adapter = p
				break
			}
		}
	}

	var composer *PromptComposer
	if deps.OcDB != nil {
		composer = NewPromptComposer(deps.OcDB)
	} else {
		composer = NewPromptComposer(&nullSessionReader{})
	}

	launcher := NewSessionLauncher(
		deps.StateDB,
		adapter,
		deps.CreateWorktree,
		deps.LaunchTmux,
		deps.DiscoverPort,
	)

	split := &splitTools{
		composer: composer,
		launcher: launcher,
		platform: deps.PlatformID,
	}

	var ocDB statusSessionReader
	if deps.OcDB != nil {
		ocDB = deps.OcDB
	}
	status := &statusTools{
		stateDB: deps.StateDB,
		ocDB:    ocDB,
	}

	return []mcpserver.ServerTool{
		{Tool: splitToSessionTool(), Handler: split.handleSplitToSession},
		{Tool: splitToWorktreeTool(), Handler: split.handleSplitToWorktree},
		{Tool: getSessionStatusTool(), Handler: status.handleGetSessionStatus},
		{Tool: getCurrentSessionIDTool(), Handler: status.handleGetCurrentSessionID},
		{Tool: listChildSessionsTool(), Handler: status.handleListChildSessions},
		{Tool: cancelSessionTool(), Handler: status.handleCancelSession},
	}
}

// NewRawServer builds the underlying mcp-go MCPServer without wrapping
// it in an HTTP transport. Used by tests that want to call tools directly
// in-process without spinning up an HTTP server.
func NewRawServer(deps Deps) *mcpserver.MCPServer {
	if deps.CreateWorktree == nil {
		deps.CreateWorktree = worktree.Create
	}

	var adapter platformAdapter
	if deps.Registry != nil {
		for _, p := range deps.Registry.Platforms() {
			if string(p.ID()) == deps.PlatformID {
				adapter = p
				break
			}
		}
	}

	var composer *PromptComposer
	if deps.OcDB != nil {
		composer = NewPromptComposer(deps.OcDB)
	} else {
		composer = NewPromptComposer(&nullSessionReader{})
	}

	launcher := NewSessionLauncher(
		deps.StateDB,
		adapter,
		deps.CreateWorktree,
		deps.LaunchTmux,
		deps.DiscoverPort,
	)

	s := mcpserver.NewMCPServer("ocman", "1.0.0", mcpserver.WithToolCapabilities(false))

	split := &splitTools{
		composer: composer,
		launcher: launcher,
		platform: deps.PlatformID,
	}
	addSplitTools(s, split)

	var ocDB statusSessionReader
	if deps.OcDB != nil {
		ocDB = deps.OcDB
	}
	status := &statusTools{
		stateDB: deps.StateDB,
		ocDB:    ocDB,
	}
	addStatusTools(s, status)

	return s
}

// nullSessionReader is a no-op sessionReader used when the OpenCode DB
// is unavailable. It returns empty results so the composer can still
// produce a minimal prompt from the intent alone.
type nullSessionReader struct{}

func (n *nullSessionReader) GetSession(_ string) (*db.Session, error) {
	return &db.Session{}, nil
}

func (n *nullSessionReader) GetSessionMessages(_ string) ([]db.Message, error) {
	return nil, nil
}
