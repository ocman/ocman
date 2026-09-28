package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/linkpreview"
	"github.com/NoUseFreak/ocman/internal/previewauth"
)

// Viewer identity for provider consent.
//
// ocman's password is shared, so the auth cookie says "someone who knows the
// password", not who. Private preview metadata is instead scoped to a
// viewer: one browser, identified by a random HttpOnly cookie whose SHA-256
// is registered in state.db for this owner machine. A viewer is only
// resolved for a request that already has app access (a valid auth cookie,
// or — with auth off or TrustLocalhost — a direct, un-proxied loopback
// request), so an unauthenticated remote client never reaches private
// metadata. Signing out deletes the viewer and its provider grants.
//
// Owner routing (multi-remote): the hub holds viewer credentials and makes
// every provider call; nothing is routed to the remote. Instead every grant,
// pending consent and cached preview is keyed by an explicit owner — this
// machine's instance ID, or a connected remote's instance ID from remoteId —
// so a grant made for one host never answers for another, and hub grants
// never answer for remote content. The viewer (browser) identity is hub-wide
// and registered under this machine's ID. An explicit remoteId that is not
// connected fails closed (503) before any credential or cache is touched,
// and a remote's disconnect purges its cached previews.

const (
	viewerCookieName   = "ocman_viewer"
	viewerCookieTTL    = 365 * 24 * time.Hour
	previewCallback    = "/api/previews/oauth/callback"
	maxPreviewAuthBody = 4 * 1024
)

var errPreviewForbidden = errors.New("forbidden")

type previewAuthState struct {
	mu        sync.Mutex
	client    *http.Client
	providers []previewauth.Provider // from WithPreviewProviders
	resolvers []linkpreview.Resolver // from WithPreviewResolvers
	// Built lazily from apps; reset when Settings changes an app.
	manager  *previewauth.Manager
	previews *linkpreview.Service
	owners   []previewOwnerToken
	hooked   bool
	appsMu   sync.Mutex // serializes read-modify-write of saved apps
}

// WithPreviewProviders registers the OAuth applications viewers may connect.
// client may be nil. Must be called before Start.
func (s *Server) WithPreviewProviders(client *http.Client, providers ...previewauth.Provider) *Server {
	s.previewAuth.client = client
	s.previewAuth.providers = providers
	return s
}

// previewBuilt returns the current manager and service, building them from
// the configured apps on first use or after resetPreviews. In-flight
// requests keep the instance they got; pending consent lives in state.db,
// so a rebuilt manager still completes it.
func (s *Server) previewBuilt() (*previewauth.Manager, *linkpreview.Service, []previewOwnerToken) {
	st := &s.previewAuth
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.manager == nil {
		providers, resolvers, owners := s.previewSetup(s.previewApps(context.Background()))
		st.manager = previewauth.New(s.stateDB, s.publicURL(previewCallback), st.client, append(providers, st.providers...)...)
		st.previews = linkpreview.New(st.manager, st.manager.Client(), append(resolvers, st.resolvers...)...)
		st.owners = owners
		if !st.hooked {
			st.hooked = true
			s.router().OnUnregister(func(remoteID string) { s.linkPreviews().Purge("", remoteID, "") })
		}
	}
	return st.manager, st.previews, st.owners
}

// resetPreviews drops the built providers (and their preview cache) so the
// next request rebuilds them from the current apps.
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

// previewAccessAllowed gates private preview metadata.
func (s *Server) previewAccessAllowed(r *http.Request) bool {
	if s.auth == nil {
		return directLocalRequest(r)
	}
	return s.auth.hasValidCookie(r) || (s.auth.trustLocalhost && directLocalRequest(r))
}

// previewOwner resolves remoteId to an owner ID: this machine (home) or a
// connected remote. A disconnected or unknown remote fails closed.
func (s *Server) previewOwner(r *http.Request) (home, owner string, ok bool) {
	ident, err := s.stateDB.InstanceIdentity(r.Context())
	if err != nil {
		return "", "", false
	}
	switch id := r.URL.Query().Get("remoteId"); id {
	case "", "local", ident.InstanceID:
		return ident.InstanceID, ident.InstanceID, true
	default:
		if _, ok := s.router().LookupRemote(id); ok {
			return ident.InstanceID, id, true
		}
	}
	return "", "", false
}

func hashViewer(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// previewViewer resolves the request's viewer, registered on this machine
// (homeID). With create, a browser without a viewer is issued one. An empty
// ID means "no viewer".
func (s *Server) previewViewer(w http.ResponseWriter, r *http.Request, homeID string, create bool) (string, error) {
	if c, err := r.Cookie(viewerCookieName); err == nil && c.Value != "" {
		id := hashViewer(c.Value)
		ok, err := s.stateDB.PreviewViewerExists(r.Context(), id, homeID)
		if err != nil {
			return "", err
		}
		if ok {
			return id, nil
		}
	}
	if !create {
		return "", nil
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	secret := base64.RawURLEncoding.EncodeToString(b)
	id := hashViewer(secret)
	if err := s.stateDB.CreatePreviewViewer(r.Context(), id, homeID); err != nil {
		return "", err
	}
	s.setViewerCookie(w, r, secret, viewerCookieTTL)
	return id, nil
}

func (s *Server) setViewerCookie(w http.ResponseWriter, r *http.Request, value string, ttl time.Duration) {
	c := &http.Cookie{
		Name: viewerCookieName, Value: value, Path: "/", HttpOnly: true,
		// Lax: the provider's top-level redirect to the callback must carry it.
		SameSite: http.SameSiteLaxMode,
		Secure:   s.auth.cookieSecure(r) || strings.HasPrefix(strings.ToLower(s.publicBaseURL), "https://"),
		MaxAge:   int(ttl.Seconds()),
	}
	if ttl <= 0 {
		c.MaxAge, c.Expires = -1, time.Unix(0, 0)
	}
	http.SetCookie(w, c)
}

// previewContext runs the access and owner gates shared by every route.
func (s *Server) previewContext(w http.ResponseWriter, r *http.Request, create bool) (viewerID, ownerID string, ok bool) {
	if !s.previewAccessAllowed(r) {
		http.Error(w, errPreviewForbidden.Error(), http.StatusForbidden)
		return "", "", false
	}
	homeID, ownerID, ok := s.previewOwner(r)
	if !ok {
		w.Header().Set("Cache-Control", "no-store")
		http.Error(w, "owner unavailable", http.StatusServiceUnavailable)
		return "", "", false
	}
	viewerID, err := s.previewViewer(w, r, homeID, create)
	if err != nil {
		http.Error(w, "viewer unavailable", http.StatusInternalServerError)
		return "", "", false
	}
	return viewerID, ownerID, true
}

// handlePreviewProviders lists configured providers and this viewer's
// connections (display names only, never tokens).
func (s *Server) handlePreviewProviders(w http.ResponseWriter, r *http.Request) {
	viewerID, ownerID, ok := s.previewContext(w, r, false)
	if !ok {
		return
	}
	m, _, owners := s.previewBuilt()
	status, err := m.Status(r.Context(), viewerID, ownerID)
	if err != nil {
		http.Error(w, "status unavailable", http.StatusInternalServerError)
		return
	}
	if owners == nil {
		owners = []previewOwnerToken{}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, map[string]any{"providers": status, "ownerTokens": owners})
}

// handlePreviewConnect starts consent and returns the authorize URL.
func (s *Server) handlePreviewConnect(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Provider string `json:"provider"`
		ReturnTo string `json:"returnTo"`
	}
	if !readAndUnmarshal(w, r, maxPreviewAuthBody, &req) {
		return
	}
	if _, ok := s.previewManager().Provider(req.Provider); !ok {
		http.Error(w, "unknown provider", http.StatusNotFound)
		return
	}
	viewerID, ownerID, ok := s.previewContext(w, r, true)
	if !ok {
		return
	}
	authURL, err := s.previewManager().Begin(r.Context(), viewerID, ownerID, req.Provider, req.ReturnTo)
	if err != nil {
		http.Error(w, "connect unavailable", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]string{"authorizeUrl": authURL})
}

// handlePreviewCallback is the exact redirect URI registered with providers.
// The state must have been issued to this browser's viewer; anything else
// (CSRF, replay) is rejected without redirecting anywhere. The grant is
// stored for the owner recorded with the state at connect time.
func (s *Server) handlePreviewCallback(w http.ResponseWriter, r *http.Request) {
	viewerID, _, ok := s.previewContext(w, r, false)
	if !ok {
		return
	}
	q := r.URL.Query()
	returnTo, err := s.previewManager().Complete(r.Context(), viewerID, q.Get("state"), q.Get("code"), q.Get("error"))
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
	u, _ := url.Parse(previewauth.SafeReturnPath(returnTo))
	v := u.Query()
	v.Set("previewAuth", result)
	u.RawQuery = v.Encode()
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, r, u.String(), http.StatusSeeOther)
}

// handlePreviewDisconnect deletes (and best-effort revokes) a viewer's grant.
func (s *Server) handlePreviewDisconnect(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Provider    string `json:"provider"`
		WorkspaceID string `json:"workspaceId"`
	}
	if !readAndUnmarshal(w, r, maxPreviewAuthBody, &req) {
		return
	}
	viewerID, ownerID, ok := s.previewContext(w, r, false)
	if !ok {
		return
	}
	if viewerID != "" {
		err := s.previewManager().Disconnect(r.Context(), viewerID, ownerID, req.Provider, req.WorkspaceID)
		if errors.Is(err, previewauth.ErrUnknownProvider) {
			http.Error(w, "unknown provider", http.StatusNotFound)
			return
		}
		s.linkPreviews().Purge(viewerID, ownerID, req.Provider)
		if err != nil {
			http.Error(w, "disconnect failed", http.StatusInternalServerError)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// signOutPreviewViewer forgets the browser's viewer on logout.
func (s *Server) signOutPreviewViewer(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(viewerCookieName)
	if err != nil || c.Value == "" {
		return
	}
	s.linkPreviews().Purge(hashViewer(c.Value), "", "")
	if err := s.previewManager().SignOut(r.Context(), hashViewer(c.Value)); err != nil {
		log.WithError(err).Warn("preview auth: sign-out cleanup failed")
	}
	s.setViewerCookie(w, r, "", 0)
}
