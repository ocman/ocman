// Package sharing shares conversations: Markdown export, encrypted relay
// share links kept current on every completed turn, and the sharing
// setting. The relay protocol and encryption live in internal/share.
package sharing

import (
	"context"
	"net/http"
	"runtime/debug"

	log "github.com/sirupsen/logrus"

	"github.com/ocman/ocman/internal/module"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/state"
)

type Deps struct {
	Store *state.DB
	// Session resolves the adapter owning a request's session, writing the
	// error response itself when there is none.
	Session func(w http.ResponseWriter, r *http.Request, sessionID string) platforms.Platform
	// Platform is the adapter for a compound platform id.
	Platform func(platforms.ID) (platforms.Platform, bool)
	// RelayURL is the share relay, RelaySource where its value came from
	// ("flag", "env", "builtin"); both empty without a relay.
	RelayURL    func() string
	RelaySource func() string
}

type Module struct {
	Deps
	publishers module.Workers // idle-edge publishes, joined by Run
}

var (
	_ module.Module     = (*Module)(nil)
	_ module.Subscriber = (*Module)(nil)
	_ module.Starter    = (*Module)(nil)
	_ module.Runner     = (*Module)(nil)
)

func NewModule(deps Deps) *Module { return &Module{Deps: deps} }

// Start binds idle-edge publishes to the server's lifetime.
func (m *Module) Start(ctx context.Context) error {
	m.publishers.Bind(ctx)
	return nil
}

// Run joins the publishes once the server stops.
func (m *Module) Run(ctx context.Context) {
	<-ctx.Done()
	m.publishers.Close()
}

func (m *Module) Name() string { return "sharing" }

// Routes are more specific than the server's /api/session/ dispatcher, so
// the mux sends these here.
func (m *Module) Routes(r module.Router) {
	r.Handle("GET /api/session/{id}/export.md", m.withSession(m.handleExportMarkdown))
	r.Handle("GET /api/session/{id}/shares", m.withSession(m.handleSessionShares))
	r.Handle("POST /api/session/{id}/share", m.withSession(m.handleCreateSessionShare))
	r.Handle("DELETE /api/session/{id}/share/{token}", m.withSession(m.handleRevokeSessionShare))
	r.Handle("GET /api/shares", m.handleAllShares)
	r.Setting("/api/settings/sharing", m.handleSharingSetting)
}

// Subscribe appends each completed turn to the session's live shares.
func (m *Module) Subscribe(b *module.Bus) {
	b.OnSessionIdle(func(_ context.Context, e module.SessionIdle) {
		adapter, ok := m.Platform(platforms.ID(e.Platform))
		if !ok {
			return
		}
		m.publishers.Go(func(ctx context.Context) {
			defer func() {
				if r := recover(); r != nil {
					log.WithFields(log.Fields{"panic": r, "stack": string(debug.Stack()), "session_id": e.SessionID}).Error("share relay publisher panicked")
				}
			}()
			if err := m.PublishCompletedTurn(ctx, adapter, e.SessionID); err != nil {
				log.WithError(err).WithField("session_id", e.SessionID).Warn("publishing completed turn to share relay")
			}
		})
	})
}

type sessionHandler func(w http.ResponseWriter, r *http.Request, sessionID string, adapter platforms.Platform)

func (m *Module) withSession(h sessionHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if adapter := m.Session(w, r, id); adapter != nil {
			h(w, r, id, adapter)
		}
	}
}
