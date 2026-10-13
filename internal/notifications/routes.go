package notifications

import (
	"crypto/ecdh"
	"encoding/base64"
	"net/http"
	"net/url"
	"strings"

	"github.com/NoUseFreak/ocman/internal/httpx"
	webpush "github.com/SherClockHolmes/webpush-go"
)

// Browser-provided URLs still cross a trust boundary. Only known push services
// may receive requests; never let a subscription become an arbitrary URL fetch.
func validEndpoint(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 4096 || u.Scheme != "https" || u.User != nil || u.Fragment != "" || (u.Port() != "" && u.Port() != "443") {
		return false
	}
	h := u.Hostname()
	return h == "fcm.googleapis.com" || h == "updates.push.services.mozilla.com" || strings.HasSuffix(h, ".push.apple.com") || strings.HasSuffix(h, ".notify.windows.com")
}

func validSubscription(s webpush.Subscription) bool {
	key, err := base64.RawURLEncoding.DecodeString(s.Keys.P256dh)
	if err != nil {
		return false
	}
	if _, err := ecdh.P256().NewPublicKey(key); err != nil {
		return false
	}
	auth, err := base64.RawURLEncoding.DecodeString(s.Keys.Auth)
	return err == nil && len(auth) == 16 && validEndpoint(s.Endpoint)
}

func (m *Module) available(w http.ResponseWriter, r *http.Request) bool {
	if m.store.db == nil {
		http.Error(w, "Web Push is unavailable", http.StatusServiceUnavailable)
		return false
	}
	// POSTs require a same-origin browser request, including when auth is off.
	if r.Method == http.MethodPost {
		u, err := url.Parse(r.Header.Get("Origin"))
		if err != nil || u.Host != r.Host || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil {
			http.Error(w, "same-origin request required", http.StatusForbidden)
			return false
		}
	}
	return true
}

func (m *Module) publicKey(w http.ResponseWriter, r *http.Request) {
	if !m.available(w, r) {
		return
	}
	_, public, err := m.store.keys(r.Context())
	if err != nil {
		httpx.ServerError(w, "reading Web Push key", err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, map[string]string{"publicKey": public})
}

func (m *Module) subscribe(w http.ResponseWriter, r *http.Request) {
	if !m.available(w, r) {
		return
	}
	var sub webpush.Subscription
	if !httpx.ReadJSON(w, r, 8192, &sub) {
		return
	}
	if !validSubscription(sub) {
		http.Error(w, "invalid push subscription", http.StatusBadRequest)
		return
	}
	if _, _, err := m.store.keys(r.Context()); err != nil {
		httpx.ServerError(w, "reading Web Push key", err)
		return
	}
	if err := m.store.save(r.Context(), sub); err != nil {
		httpx.ServerError(w, "saving push subscription", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (m *Module) unsubscribe(w http.ResponseWriter, r *http.Request) {
	if !m.available(w, r) {
		return
	}
	var input struct {
		Endpoint string `json:"endpoint"`
	}
	if !httpx.ReadJSON(w, r, 8192, &input) {
		return
	}
	if !validEndpoint(input.Endpoint) {
		http.Error(w, "invalid push endpoint", http.StatusBadRequest)
		return
	}
	if err := m.store.remove(r.Context(), input.Endpoint); err != nil {
		httpx.ServerError(w, "removing push subscription", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
