package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/linkpreview"
	"github.com/NoUseFreak/ocman/internal/previewauth"
)

// Link previews are resolved on this machine (the hub) with this machine's
// credentials: CLI/env forge tokens, personal tokens pasted in Settings, and
// OAuth grants. ocman is a personal tool, so every credential belongs to the
// machine, not to a browser: any request with app access (a valid auth
// cookie, or a direct loopback request when auth is off or trusts
// localhost) may use them, including for private resources. A request
// without app access only sees public forge previews.
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

type previewAuthState struct {
	mu        sync.Mutex
	client    *http.Client
	providers []previewauth.Provider // from WithPreviewProviders
	resolvers []linkpreview.Resolver // from WithPreviewResolvers
	// Built lazily; reset when Settings changes a token or app.
	manager  *previewauth.Manager
	previews *linkpreview.Service
	catalog  []previewEntry
	appsMu   sync.Mutex // serializes read-modify-write of saved apps
}

// WithPreviewProviders registers extra providers (tests). client may be
// nil. Must be called before Start.
func (s *Server) WithPreviewProviders(client *http.Client, providers ...previewauth.Provider) *Server {
	s.previewAuth.client = client
	s.previewAuth.providers = providers
	return s
}

// previewBuilt returns the current manager, service and catalog, building
// them on first use or after resetPreviews. In-flight requests keep the
// instance they got; pending consent lives in state.db, so a rebuilt
// manager still completes it.
func (s *Server) previewBuilt() (*previewauth.Manager, *linkpreview.Service, []previewEntry) {
	st := &s.previewAuth
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.manager == nil {
		ctx := context.Background()
		grants := map[string]bool{}
		if home, err := s.stateDB.InstanceIdentity(ctx); err == nil {
			creds, err := s.stateDB.PreviewCredentials(ctx, machineViewer, home.InstanceID, "")
			if err != nil {
				log.WithError(err).Warn("preview auth: reading grants failed")
			}
			for _, c := range creds {
				grants[c.Provider] = true
			}
		}
		providers, resolvers, catalog := s.previewSetup(s.previewApps(ctx), grants)
		for _, p := range st.providers {
			catalog = append(catalog, previewEntry{ID: p.ID, Name: p.Name, Configured: grants[p.ID]})
		}
		st.manager = previewauth.New(s.stateDB, s.publicURL(previewCallback), st.client, append(providers, st.providers...)...)
		st.previews = linkpreview.New(st.manager, st.manager.Client(), append(resolvers, st.resolvers...)...)
		st.catalog = catalog
	}
	return st.manager, st.previews, st.catalog
}

// resetPreviews drops the built providers (and their preview cache) so the
// next request rebuilds them from the current tokens and apps.
func (s *Server) resetPreviews() {
	s.previewAuth.mu.Lock()
	s.previewAuth.manager, s.previewAuth.previews = nil, nil
	s.previewAuth.mu.Unlock()
}

func (s *Server) previewManager() *previewauth.Manager {
	m, _, _ := s.previewBuilt()
	return m
}

// directLocalRequest is a loopback peer that did not come through a proxy:
// behind a reverse proxy every request has a loopback RemoteAddr.
func directLocalRequest(r *http.Request) bool {
	if !isLoopback(r) || r.Header.Get("X-Forwarded-For") != "" || r.Header.Get("Forwarded") != "" || r.Header.Get("X-Real-Ip") != "" {
		return false
	}
	host, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		host = r.Host
	}
	return isLoopbackHostname(strings.Trim(host, "[]"))
}

// previewAccessAllowed: the request may use this machine's credentials.
func (s *Server) previewAccessAllowed(r *http.Request) bool {
	if s.auth == nil {
		return directLocalRequest(r)
	}
	return s.auth.hasValidCookie(r) || (s.auth.trustLocalhost && directLocalRequest(r))
}

// previewHome is this machine's instance ID, after checking that an
// explicit remoteId names this machine or a connected remote.
func (s *Server) previewHome(r *http.Request) (string, bool) {
	ident, err := s.stateDB.InstanceIdentity(r.Context())
	if err != nil {
		return "", false
	}
	switch id := r.URL.Query().Get("remoteId"); id {
	case "", "local", ident.InstanceID:
		return ident.InstanceID, true
	default:
		_, ok := s.router().LookupRemote(id)
		return ident.InstanceID, ok
	}
}

// previewContext runs the access and owner gates shared by credential routes.
func (s *Server) previewContext(w http.ResponseWriter, r *http.Request) (string, bool) {
	if !s.previewAccessAllowed(r) {
		http.Error(w, errPreviewForbidden.Error(), http.StatusForbidden)
		return "", false
	}
	home, ok := s.previewHome(r)
	if !ok {
		w.Header().Set("Cache-Control", "no-store")
		http.Error(w, "owner unavailable", http.StatusServiceUnavailable)
		return "", false
	}
	return home, true
}

// handlePreviewConnect starts OAuth consent and returns the authorize URL.
func (s *Server) handlePreviewConnect(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Provider string `json:"provider"`
		ReturnTo string `json:"returnTo"`
	}
	if !readAndUnmarshal(w, r, maxPreviewAuthBody, &req) {
		return
	}
	home, ok := s.previewContext(w, r)
	if !ok {
		return
	}
	authURL, err := s.previewManager().Begin(r.Context(), machineViewer, home, req.Provider, req.ReturnTo)
	switch {
	case errors.Is(err, previewauth.ErrUnknownProvider), errors.Is(err, previewauth.ErrNoOAuth):
		http.Error(w, "provider has no sign-in app", http.StatusNotFound)
	case err != nil:
		http.Error(w, "connect unavailable", http.StatusInternalServerError)
	default:
		writeJSON(w, map[string]string{"authorizeUrl": authURL})
	}
}

// handlePreviewCallback is the exact redirect URI registered with
// providers. The one-time state must have been issued by this machine; a
// forged or replayed one is rejected without redirecting anywhere.
func (s *Server) handlePreviewCallback(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.previewContext(w, r); !ok {
		return
	}
	q := r.URL.Query()
	returnTo, err := s.previewManager().Complete(r.Context(), machineViewer, q.Get("state"), q.Get("code"), q.Get("error"))
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
	s.resetPreviews()
	u, _ := url.Parse(previewauth.SafeReturnPath(returnTo))
	v := u.Query()
	v.Set("previewAuth", result)
	u.RawQuery = v.Encode()
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, r, u.String(), http.StatusSeeOther)
}

// handlePreviewDisconnect deletes (and best-effort revokes) a grant.
func (s *Server) handlePreviewDisconnect(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Provider    string `json:"provider"`
		WorkspaceID string `json:"workspaceId"`
	}
	if !readAndUnmarshal(w, r, maxPreviewAuthBody, &req) {
		return
	}
	home, ok := s.previewContext(w, r)
	if !ok {
		return
	}
	err := s.previewManager().Disconnect(r.Context(), machineViewer, home, req.Provider, req.WorkspaceID)
	if errors.Is(err, previewauth.ErrUnknownProvider) {
		http.Error(w, "unknown provider", http.StatusNotFound)
		return
	}
	s.resetPreviews()
	if err != nil {
		http.Error(w, "disconnect failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
