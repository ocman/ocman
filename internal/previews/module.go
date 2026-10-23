// Package previews is the link-preview feature module: the provider catalog,
// sign-in apps, pasted tokens and OAuth grants, the custom link rules, the
// resolve endpoint that turns transcript text into previews, and the forge
// integration status.
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

	"github.com/NoUseFreak/ocman/internal/httpx"
	"github.com/NoUseFreak/ocman/internal/linkpreview"
	"github.com/NoUseFreak/ocman/internal/module"
	"github.com/NoUseFreak/ocman/internal/previewauth"
	"github.com/NoUseFreak/ocman/internal/state"
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
	Guards module.Guards

	// Test seams: extra providers/resolvers and the provider HTTP client
	// (nil = previewauth's default).
	Client    *http.Client
	Providers []previewauth.Provider
	Resolvers []linkpreview.Resolver
}

// Module serves /api/previews/*, /api/settings/link-preview-rules and
// /api/integrations/status.
type Module struct {
	Deps
	built builtState
}

func NewModule(d Deps) *Module { return &Module{Deps: d} }

func (m *Module) Name() string { return "previews" }

// Routes keeps the method check outside the auth guard, as the server's
// method-pattern routes do, so a wrong method is 405 for every caller.
func (m *Module) Routes(r module.Router) {
	auth := m.Guards.Auth
	get := func(h http.HandlerFunc) http.HandlerFunc { return only(http.MethodGet, auth(m.withStore(h))) }
	post := func(h http.HandlerFunc) http.HandlerFunc { return only(http.MethodPost, auth(m.withStore(h))) }
	r.Handle("/api/integrations/status", only(http.MethodGet, auth(m.handleIntegrationsStatus)))
	// Machine credentials for private previews (auth.go).
	r.Handle("/api/previews/providers", get(m.handlePreviewProviders))
	r.Handle("/api/previews/connect", post(m.handlePreviewConnect))
	r.Handle("/api/previews/disconnect", post(m.handlePreviewDisconnect))
	r.Handle("/api/previews/resolve", post(m.handlePreviewResolve))
	r.Handle("/api/previews/image", get(m.handlePreviewImage))
	r.Handle("/api/previews/token", post(m.handlePreviewToken))
	r.Handle("/api/previews/apps", get(m.handlePreviewApps))
	r.Handle("/api/previews/apps/save", post(m.handleSavePreviewApp))
	r.Handle("/api/previews/apps/remove", post(m.handleRemovePreviewApp))
	r.Handle(previewCallback, get(m.handlePreviewCallback))
	r.Setting("/api/settings/link-preview-rules", auth(m.withStore(m.handleLinkPreviewRules)))
}

func only(method string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != method {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		h(w, r)
	}
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

// handleIntegrationsStatus reports whether the hub's GitHub client is
// authenticated and the configured Forgejo hosts, so the frontend knows
// which hostnames to scan for previewable links (GitHub's host is fixed;
// Forgejo's are dynamic).
func (m *Module) handleIntegrationsStatus(w http.ResponseWriter, _ *http.Request) {
	f := m.forges()
	hosts := []string{}
	for _, l := range f.Forgejo {
		hosts = append(hosts, l.Host)
	}
	httpx.WriteJSON(w, map[string]any{
		"github":  map[string]any{"available": true, "authenticated": f.GitHubToken != ""},
		"forgejo": map[string]any{"available": len(hosts) > 0, "hosts": hosts},
	})
}
