// Package inbox is the owner-local Inbox: items an agent, Factory or a
// pending permission raises for the user, listed across every connected
// machine. Items live in internal/state.
package inbox

import (
	"context"
	"time"

	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/ocman/ocman/internal/module"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/state"
)

const (
	fanoutLimit = 8
	// remoteTimeout bounds one remote's answer so a slow machine cannot
	// hold the whole list.
	remoteTimeout         = 2 * time.Second
	reconcileInterval     = 30 * time.Second
	permissionCallTimeout = 5 * time.Second
)

// Remotes is the Inbox of every connected machine, "local" included.
type Remotes interface {
	InboxSources() []string
	InboxItems(ctx context.Context, source string) ([]state.InboxItem, error)
	ArchivedInboxItems(ctx context.Context, source string) ([]state.InboxItem, error)
	MarkInboxItemRead(ctx context.Context, source, id string) error
	MarkInboxItemUnread(ctx context.Context, source, id string) error
	ArchiveInboxItems(ctx context.Context, source string, ids []string) error
}

type Deps struct {
	Store *state.DB
	// Remotes is nil on a single-host install; the Inbox is then local only.
	Remotes func() Remotes
	// Platform finds the adapter that owns a pending permission.
	Platform func(platforms.ID) (platforms.Platform, bool)
	// EnsurePending re-raises live prompts the server has not tracked yet.
	EnsurePending func(platforms.Platform, string, []platforms.LivePrompt)
	// SessionTitle is a local session's title, "" when unknown.
	SessionTitle func(ctx context.Context, sessionID string) string
	Broadcast    func(event string, data []byte)
}

type Module struct{ Deps }

var (
	_ module.Module       = (*Module)(nil)
	_ module.Runner       = (*Module)(nil)
	_ module.ToolProvider = (*Module)(nil)
)

func NewModule(deps Deps) *Module { return &Module{deps} }

func (m *Module) Name() string { return "inbox" }

func (m *Module) Routes(r module.Router) {
	r.Handle("GET /api/inbox", m.handleInboxList)
	r.Handle("POST /api/inbox/open", m.handleInboxRead)
	r.Handle("POST /api/inbox/unread", m.handleInboxRead)
	r.Handle("POST /api/inbox/pin", m.handleInboxPin)
	r.Handle("POST /api/inbox/archive", m.handleInboxArchive)
}

// Run reconciles permission items with the live prompts until ctx ends.
func (m *Module) Run(ctx context.Context) {
	ticker := time.NewTicker(reconcileInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.Reconcile(ctx)
		}
	}
}

func (m *Module) Tools() []mcpserver.ServerTool {
	return inboxServerTools(&inboxTools{store: m.Store})
}

func (m *Module) changed() { m.Broadcast("ocman.inbox.changed", []byte(`{}`)) }
