package mcp

import (
	"context"
	"net/http"

	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/state"
)

// Deps holds the dependencies injected into the MCP server by the
// ocman server package. All fields are required unless noted.
type Deps struct {
	// OcDB is the read-only OpenCode database handle. May be nil when
	// the OpenCode platform adapter is not registered.
	OcDB *db.DB

	// StateDB is the writable ocman state database.
	StateDB *state.DB

	// Platform is the session client used for CreateSession /
	// SendMessage calls. The server package injects the shared session
	// service bound to the OpenCode platform id.
	Platform SessionClient

	// PlatformID is the platform identifier to use for child sessions
	// (e.g. "opencode").
	PlatformID string

	// CreateWorktreeSession creates a worktree plus its session on the
	// host that owns the project directory. The server package injects
	// an adapter over the owner-resolved hostsvc.Host (AD-16). Nil makes
	// worktree splits fail closed instead of running git on the hub.
	CreateWorktreeSession WorktreeSessionCreator

	// GitContext reads branch + diffstat prompt context from the host
	// that owns a directory. Nil omits the git enrichment.
	GitContext GitContextReader

	// KillTmuxTarget kills a legacy child's tmux target on the host that
	// owns it. Nil skips the kill.
	KillTmuxTarget TmuxTargetKiller

	// EnsureProjectOpencode guarantees the project's single opencode
	// instance is running for a directory and returns its port. The
	// server package injects an adapter over the owning host's
	// EnsureProjectOpencode (#267/#268). Same-directory and worktree
	// splits use it so they self-heal instead of returning
	// ErrPlatformUnreachable.
	EnsureProjectOpencode ProjectOpencodeEnsurer

	// WorkflowService drives workflow authoring and run-control tools.
	// Optional: nil disables workflow tools.
	WorkflowService workflowService

	// ChildResults makes new_session wait for the background watcher and
	// return the child's terminal result. Nil disables synchronous waits.
	ChildResults *ChildResultBroker

	// ChildDisconnected queues recovery guidance for the parent when a
	// synchronous child-result request disconnects.
	ChildDisconnected func(context.Context, string)
	// ChildStarted starts or restores event-driven completion tracking after
	// a child is created or receives a follow-up turn.
	ChildStarted func(string)

	// SignFile mints a browser-reachable URL for a file on disk, backing
	// the embed_file tool. Optional: nil makes embed_file report that
	// file embedding is unavailable.
	SignFile FileSigner
}

// Server wraps the mcp-go MCPServer and exposes an http.Handler.
type Server struct {
	handler http.Handler
}

// New constructs a Server from the given dependencies, registers all
// MCP tools, and wraps the result in a StreamableHTTPServer.
func New(deps Deps) *Server {
	adapter := deps.Platform

	composer := newComposer(deps)

	// Build the session launcher.
	launcher := NewSessionLauncher(
		deps.StateDB,
		adapter,
		deps.CreateWorktreeSession,
		deps.EnsureProjectOpencode,
	).WithChildResults(deps.ChildResults).WithChildStarted(deps.ChildStarted)

	// Build the mcp-go server.
	s := mcpserver.NewMCPServer(
		"ocman",
		"1.0.0",
		mcpserver.WithToolCapabilities(false),
	)

	// Register split tools.
	split := &splitTools{
		composer:     composer,
		launcher:     launcher,
		platform:     deps.PlatformID,
		inherit:      inheritProvider(deps.StateDB),
		results:      deps.ChildResults,
		store:        deps.StateDB,
		disconnected: deps.ChildDisconnected,
	}
	addSplitTools(s, split)

	// Register status tools.
	var ocDB statusSessionReader
	if deps.OcDB != nil {
		ocDB = deps.OcDB
	}
	status := &statusTools{
		stateDB:  deps.StateDB,
		ocDB:     ocDB,
		killTmux: deps.KillTmuxTarget,
	}
	addStatusTools(s, status)

	comm := &commTools{
		stateDB:      deps.StateDB,
		platform:     adapter,
		results:      deps.ChildResults,
		disconnected: deps.ChildDisconnected,
		started:      deps.ChildStarted,
	}
	addCommTools(s, comm)

	addFileTools(s, &fileTools{sign: deps.SignFile})

	addWorkflowTools(s, &workflowTools{svc: deps.WorkflowService})

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
	adapter := deps.Platform

	composer := newComposer(deps)

	launcher := NewSessionLauncher(
		deps.StateDB,
		adapter,
		deps.CreateWorktreeSession,
		deps.EnsureProjectOpencode,
	).WithChildResults(deps.ChildResults).WithChildStarted(deps.ChildStarted)

	split := &splitTools{
		composer:     composer,
		launcher:     launcher,
		platform:     deps.PlatformID,
		inherit:      inheritProvider(deps.StateDB),
		results:      deps.ChildResults,
		store:        deps.StateDB,
		disconnected: deps.ChildDisconnected,
	}

	var ocDB statusSessionReader
	if deps.OcDB != nil {
		ocDB = deps.OcDB
	}
	status := &statusTools{
		stateDB:  deps.StateDB,
		ocDB:     ocDB,
		killTmux: deps.KillTmuxTarget,
	}
	comm := &commTools{
		stateDB:      deps.StateDB,
		platform:     adapter,
		results:      deps.ChildResults,
		disconnected: deps.ChildDisconnected,
		started:      deps.ChildStarted,
	}

	tools := []mcpserver.ServerTool{
		{Tool: newSessionTool(), Handler: split.handleNewSession},
		{Tool: awaitSessionResultTool(), Handler: split.handleAwaitSessionResult},
		{Tool: getSessionStatusTool(), Handler: status.handleGetSessionStatus},
		{Tool: getCurrentSessionIDTool(), Handler: status.handleGetCurrentSessionID},
		{Tool: listChildSessionsTool(), Handler: status.handleListChildSessions},
		{Tool: cancelSessionTool(), Handler: status.handleCancelSession},
		{Tool: sendMessageToChildTool(), Handler: comm.handleSendMessageToChild},
		{Tool: sendMessageToParentTool(), Handler: comm.handleSendMessageToParent},
		{Tool: embedFileTool(), Handler: (&fileTools{sign: deps.SignFile}).handleEmbedFile},
	}
	tools = append(tools, workflowServerTools(&workflowTools{svc: deps.WorkflowService})...)
	return tools
}

// newComposer builds the prompt composer: the OpenCode DB for session
// history, and the owner-routed git reader for branch/diffstat context.
// A nil DB yields minimal prompts (intent only).
func newComposer(deps Deps) *PromptComposer {
	var reader sessionReader = &nullSessionReader{}
	if deps.OcDB != nil {
		reader = deps.OcDB
	}
	return NewPromptComposer(reader, deps.GitContext)
}

// inheritProvider returns the permission-inheritance dependency for the
// split tools, or a nil interface when no state DB is available (so the
// split tools skip inheritance instead of calling methods on a nil
// *state.DB and panicking).
func inheritProvider(db *state.DB) permissionInheriter {
	if db == nil {
		return nil
	}
	return db
}

// nullSessionReader is a no-op sessionReader used when the OpenCode DB
// is unavailable. It returns empty results so the composer can still
// produce a minimal prompt from the intent alone.
type nullSessionReader struct{}

func (n *nullSessionReader) GetSession(context.Context, string) (*db.Session, error) {
	return &db.Session{}, nil
}

func (n *nullSessionReader) GetSessionMessages(context.Context, string) ([]db.Message, error) {
	return nil, nil
}
