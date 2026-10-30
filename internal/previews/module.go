// Package previews is the link-preview feature module: the provider catalog,
// sign-in apps, pasted tokens and OAuth grants, the custom link rules, the
// resolve endpoint that turns transcript text into previews.
//
// Previews resolve on this machine (the hub) with this machine's
// credentials; nothing is routed to remotes. Fetching, caching, rate-limit
// backoff and PR status freshness live in internal/linkpreview; grant
// storage and consent in internal/previewauth. The hub's forge clients stay
// server-owned (projectforge and factory share them); this module only sees
// their CLI/env credentials through Deps.Forges.
package previews

import (
	"net/http"

	"github.com/ocman/ocman/internal/linkpreview"
	"github.com/ocman/ocman/internal/module"
	"github.com/ocman/ocman/internal/previewauth"
	"github.com/ocman/ocman/internal/state"
)

// Forges are the hub's CLI/env forge credentials (gh, tea, *_TOKEN).
type Forges struct {
	GitHubToken string
	Forgejo     []ForgejoLogin
}

// ForgejoLogin is one configured Forgejo client.
type ForgejoLogin struct {
	Host, BaseURL, Token string
}

// Deps is what the composition root supplies. Functions are read at call
// time, so auth, connected remotes and forge logins may change after the
// module is built.
type Deps struct {
	// Store holds rules, sealed apps and grants; nil answers 503.
	Store *state.DB
	// Access reports whether the request may use this machine's
	// credentials (app access: a valid cookie, or a direct loopback
	// request when auth is off or trusts localhost).
	Access func(*http.Request) bool
	// RemoteConnected reports whether a remote ID is connected.
	RemoteConnected func(id string) bool
	// PublicURL makes an absolute URL (the OAuth redirect URI).
	PublicURL func(path string) string
	// Forges returns the hub's forge credentials; nil means none.
	Forges func() Forges

	// Test seams: extra providers/resolvers and the provider HTTP client
	// (nil = previewauth's default).
	Client    *http.Client
	Providers []previewauth.Provider
	Resolvers []linkpreview.Resolver
}

// Module serves /api/previews/* and /api/settings/link-preview-rules.
type Module struct {
	Deps
	built builtState
}

func NewModule(d Deps) *Module { return &Module{Deps: d} }

func (m *Module) Name() string { return "previews" }

// Routes keeps the method check outside the auth guard, as the server's
// method-pattern routes do, so a wrong method is 405 for every caller.
func (m *Module) Routes(r module.Router) {
	guard := m.withStore
	// Machine credentials for private previews (auth.go).
	r.Handle("GET /api/previews/providers", guard(m.handlePreviewProviders))
	r.Handle("POST /api/previews/connect", guard(m.handlePreviewConnect))
	r.Handle("POST /api/previews/disconnect", guard(m.handlePreviewDisconnect))
	r.Handle("POST /api/previews/resolve", guard(m.handlePreviewResolve))
	r.Handle("GET /api/previews/image", guard(m.handlePreviewImage))
	r.Handle("GET /api/previews/external-image", m.handleExternalImage)
	r.Handle("POST /api/previews/token", guard(m.handlePreviewToken))
	r.Handle("GET /api/previews/apps", guard(m.handlePreviewApps))
	r.Handle("POST /api/previews/apps/save", guard(m.handleSavePreviewApp))
	r.Handle("POST /api/previews/apps/remove", guard(m.handleRemovePreviewApp))
	r.Handle("GET "+previewCallback, guard(m.handlePreviewCallback))
	r.Setting("/api/settings/link-preview-rules", m.withStore(m.handleLinkPreviewRules))
}

func (m *Module) withStore(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if m.Store == nil {
			http.Error(w, "state database not available", http.StatusServiceUnavailable)
			return
		}
		h(w, r)
	}
}

func (m *Module) forges() Forges {
	if m.Forges == nil {
		return Forges{}
	}
	return m.Forges()
}
