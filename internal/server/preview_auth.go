package server

import (
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
// Owner routing: every endpoint takes remoteId. Only this machine ("",
// "local" or its own instance ID) is served here; any other owner fails
// closed until owner-routed previews exist.

const (
	viewerCookieName   = "ocman_viewer"
	viewerCookieTTL    = 365 * 24 * time.Hour
	previewCallback    = "/api/previews/oauth/callback"
	maxPreviewAuthBody = 4 * 1024
)

var errPreviewForbidden = errors.New("forbidden")

type previewAuthState struct {
	once      sync.Once
	client    *http.Client
	providers []previewauth.Provider
	manager   *previewauth.Manager
	resolvers []linkpreview.Resolver
	previews  *linkpreview.Service
}

// WithPreviewProviders registers the OAuth applications viewers may connect.
// client may be nil. Must be called before Start.
func (s *Server) WithPreviewProviders(client *http.Client, providers ...previewauth.Provider) *Server {
	s.previewAuth.client = client
	s.previewAuth.providers = providers
	return s
}

func (s *Server) previewManager() *previewauth.Manager {
	s.previewAuth.once.Do(func() {
		s.previewAuth.manager = previewauth.New(s.stateDB, s.publicURL(previewCallback), s.previewAuth.client, s.previewAuth.providers...)
		s.previewAuth.previews = linkpreview.New(s.previewAuth.manager, s.previewAuth.manager.Client(), s.previewAuth.resolvers...)
	})
	return s.previewAuth.manager
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

// previewOwner resolves remoteId to this machine's owner ID, failing closed
// for any other machine.
func (s *Server) previewOwner(r *http.Request) (string, bool) {
	ident, err := s.stateDB.InstanceIdentity(r.Context())
	if err != nil {
		return "", false
	}
	switch r.URL.Query().Get("remoteId") {
	case "", "local", ident.InstanceID:
		return ident.InstanceID, true
	}
	return "", false
}

func hashViewer(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// previewViewer resolves the request's viewer for owner. With create, a
// browser without a viewer is issued one. An empty ID means "no viewer".
func (s *Server) previewViewer(w http.ResponseWriter, r *http.Request, ownerID string, create bool) (string, error) {
	if c, err := r.Cookie(viewerCookieName); err == nil && c.Value != "" {
		id := hashViewer(c.Value)
		ok, err := s.stateDB.PreviewViewerExists(r.Context(), id, ownerID)
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
	if err := s.stateDB.CreatePreviewViewer(r.Context(), id, ownerID); err != nil {
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
	ownerID, ok = s.previewOwner(r)
	if !ok {
		http.Error(w, "owner unavailable", http.StatusServiceUnavailable)
		return "", "", false
	}
	viewerID, err := s.previewViewer(w, r, ownerID, create)
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
	status, err := s.previewManager().Status(r.Context(), viewerID, ownerID)
	if err != nil {
		http.Error(w, "status unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, map[string]any{"providers": status})
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
// The state must have been issued to this browser's viewer on this owner;
// anything else (CSRF, replay, another machine) is rejected without
// redirecting anywhere.
func (s *Server) handlePreviewCallback(w http.ResponseWriter, r *http.Request) {
	viewerID, ownerID, ok := s.previewContext(w, r, false)
	if !ok {
		return
	}
	q := r.URL.Query()
	returnTo, err := s.previewManager().Complete(r.Context(), viewerID, ownerID, q.Get("state"), q.Get("code"), q.Get("error"))
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
	if ident, err := s.stateDB.InstanceIdentity(r.Context()); err == nil {
		s.linkPreviews().Purge(hashViewer(c.Value), ident.InstanceID, "")
		if err := s.previewManager().SignOut(r.Context(), hashViewer(c.Value), ident.InstanceID); err != nil {
			log.WithError(err).Warn("preview auth: sign-out cleanup failed")
		}
	}
	s.setViewerCookie(w, r, "", 0)
}
