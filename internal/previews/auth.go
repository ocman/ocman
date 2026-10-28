package previews

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"sync"

	log "github.com/sirupsen/logrus"

	"github.com/ocman/ocman/internal/httpx"
	"github.com/ocman/ocman/internal/linkpreview"
	"github.com/ocman/ocman/internal/previewauth"
)

// Link previews are resolved on this machine (the hub) with this machine's
// credentials: CLI/env forge tokens, personal tokens pasted in Settings, and
// OAuth grants. ocman is a personal tool, so every credential belongs to the
// machine, not to a browser: any request with app access (Deps.Access) may
// use them, including for private resources. A request without app access
// only sees public forge previews.
//
// Nothing is routed to remotes; previews of a remote's sessions use the
// hub's credentials too. An explicit remoteId that is not connected still
// fails closed (503).

const (
	previewCallback    = "/api/previews/oauth/callback"
	maxPreviewAuthBody = 8 * 1024
	// machineViewer keys every stored grant: one set per machine.
	machineViewer = "machine"
)

var errPreviewForbidden = errors.New("forbidden")

type builtState struct {
	mu sync.Mutex
	// Built lazily; reset when Settings changes a token or app.
	manager  *previewauth.Manager
	previews *linkpreview.Service
	catalog  []previewEntry
	appsMu   sync.Mutex // serializes read-modify-write of saved apps
}

// previewBuilt returns the current manager, service and catalog, building
// them on first use or after resetPreviews. In-flight requests keep the
// instance they got; pending consent lives in state.db, so a rebuilt
// manager still completes it.
func (m *Module) previewBuilt() (*previewauth.Manager, *linkpreview.Service, []previewEntry) {
	st := &m.built
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.manager == nil {
		ctx := context.Background()
		grants := map[string]bool{}
		if home, err := m.Store.InstanceIdentity(ctx); err == nil {
			creds, err := m.Store.PreviewCredentials(ctx, machineViewer, home.InstanceID, "")
			if err != nil {
				log.WithError(err).Warn("preview auth: reading grants failed")
			}
			for _, c := range creds {
				grants[c.Provider] = true
			}
		}
		providers, resolvers, catalog := m.previewSetup(m.previewApps(ctx), grants)
		for _, p := range m.Providers {
			catalog = append(catalog, previewEntry{ID: p.ID, Name: p.Name, Configured: grants[p.ID]})
		}
		st.manager = previewauth.New(m.Store, m.PublicURL(previewCallback), m.Client, append(providers, m.Providers...)...)
		st.previews = linkpreview.New(st.manager, st.manager.Client(), append(resolvers, m.Resolvers...)...)
		st.catalog = catalog
	}
	return st.manager, st.previews, st.catalog
}

// resetPreviews drops the built providers (and their preview cache) so the
// next request rebuilds them from the current tokens and apps.
func (m *Module) resetPreviews() {
	m.built.mu.Lock()
	m.built.manager, m.built.previews = nil, nil
	m.built.mu.Unlock()
}

func (m *Module) previewManager() *previewauth.Manager {
	mgr, _, _ := m.previewBuilt()
	return mgr
}

// previewAccessAllowed: the request may use this machine's credentials.
func (m *Module) previewAccessAllowed(r *http.Request) bool {
	return m.Access != nil && m.Access(r)
}

// previewHome is this machine's instance ID, after checking that an
// explicit remoteId names this machine or a connected remote.
func (m *Module) previewHome(r *http.Request) (string, bool) {
	ident, err := m.Store.InstanceIdentity(r.Context())
	if err != nil {
		return "", false
	}
	switch id := r.URL.Query().Get("remoteId"); id {
	case "", "local", ident.InstanceID:
		return ident.InstanceID, true
	default:
		return ident.InstanceID, m.RemoteConnected != nil && m.RemoteConnected(id)
	}
}

// previewContext runs the access and owner gates shared by credential routes.
func (m *Module) previewContext(w http.ResponseWriter, r *http.Request) (string, bool) {
	if !m.previewAccessAllowed(r) {
		http.Error(w, errPreviewForbidden.Error(), http.StatusForbidden)
		return "", false
	}
	home, ok := m.previewHome(r)
	if !ok {
		w.Header().Set("Cache-Control", "no-store")
		http.Error(w, "owner unavailable", http.StatusServiceUnavailable)
		return "", false
	}
	return home, true
}

// handlePreviewConnect starts OAuth consent and returns the authorize URL.
func (m *Module) handlePreviewConnect(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Provider string `json:"provider"`
		ReturnTo string `json:"returnTo"`
	}
	if !httpx.ReadJSON(w, r, maxPreviewAuthBody, &req) {
		return
	}
	home, ok := m.previewContext(w, r)
	if !ok {
		return
	}
	authURL, err := m.previewManager().Begin(r.Context(), machineViewer, home, req.Provider, req.ReturnTo)
	switch {
	case errors.Is(err, previewauth.ErrUnknownProvider), errors.Is(err, previewauth.ErrNoOAuth):
		http.Error(w, "provider has no sign-in app", http.StatusNotFound)
	case err != nil:
		http.Error(w, "connect unavailable", http.StatusInternalServerError)
	default:
		httpx.WriteJSON(w, map[string]string{"authorizeUrl": authURL})
	}
}

// handlePreviewCallback is the exact redirect URI registered with
// providers. The one-time state must have been issued by this machine; a
// forged or replayed one is rejected without redirecting anywhere.
func (m *Module) handlePreviewCallback(w http.ResponseWriter, r *http.Request) {
	if _, ok := m.previewContext(w, r); !ok {
		return
	}
	q := r.URL.Query()
	returnTo, err := m.previewManager().Complete(r.Context(), machineViewer, q.Get("state"), q.Get("code"), q.Get("error"))
	if errors.Is(err, previewauth.ErrInvalidState) {
		http.Error(w, "invalid authorization state", http.StatusBadRequest)
		return
	}
	result := "connected"
	if err != nil {
		// err is one of previewauth's sentinel errors: no provider body or token.
		log.WithError(err).Warn("preview auth: connect failed")
		result = "error"
	}
	m.resetPreviews()
	u, _ := url.Parse(previewauth.SafeReturnPath(returnTo))
	v := u.Query()
	v.Set("previewAuth", result)
	u.RawQuery = v.Encode()
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, r, u.String(), http.StatusSeeOther)
}

// handlePreviewDisconnect deletes (and best-effort revokes) a grant.
func (m *Module) handlePreviewDisconnect(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Provider    string `json:"provider"`
		WorkspaceID string `json:"workspaceId"`
	}
	if !httpx.ReadJSON(w, r, maxPreviewAuthBody, &req) {
		return
	}
	home, ok := m.previewContext(w, r)
	if !ok {
		return
	}
	err := m.previewManager().Disconnect(r.Context(), machineViewer, home, req.Provider, req.WorkspaceID)
	if errors.Is(err, previewauth.ErrUnknownProvider) {
		http.Error(w, "unknown provider", http.StatusNotFound)
		return
	}
	m.resetPreviews()
	if err != nil {
		http.Error(w, "disconnect failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
