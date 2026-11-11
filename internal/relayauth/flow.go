package relayauth

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
)

func (s *Service) providers(w http.ResponseWriter, _ *http.Request) {
	type choice struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	choices := make([]choice, 0, len(s.cfg.Providers))
	for _, p := range s.cfg.Providers {
		choices = append(choices, choice{p.ID, p.Name})
	}
	sendJSON(w, http.StatusOK, map[string]any{"providers": choices})
}

func (s *Service) connect(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanupLocked()
	if len(s.connections) >= maxPending {
		http.Error(w, "too many pending connections", http.StatusTooManyRequests)
		return
	}
	id, poll := randomToken(), randomToken()
	c := &connection{id: id, pollHash: digest(poll), expires: s.cfg.Now().Add(connectTTL)}
	s.connections[id] = c
	sendJSON(w, http.StatusCreated, map[string]any{"authorizationUrl": s.cfg.PublicURL + "/auth/connect/" + id, "pollToken": poll, "expiresAt": c.expires.UnixMilli()})
}

func (s *Service) poll(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanupLocked()
	hash := digest(bearer(r))
	for id, c := range s.connections {
		if subtle.ConstantTimeCompare([]byte(hash), []byte(c.pollHash)) != 1 {
			continue
		}
		if c.token == "" {
			sendJSON(w, http.StatusAccepted, map[string]string{"status": "pending"})
			return
		}
		sendJSON(w, http.StatusOK, map[string]string{"enrollmentToken": c.token})
		delete(s.connections, id)
		return
	}
	http.Error(w, "connection expired or invalid", http.StatusUnauthorized)
}

func (s *Service) choose(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanupLocked()
	c := s.connections[r.PathValue("id")]
	if c == nil || c.token != "" {
		http.Error(w, "connection expired or completed", http.StatusGone)
		return
	}
	renderPage(w, pageData{ID: c.id, Providers: s.cfg.Providers})
}

func (s *Service) startLogin(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanupLocked()
	c := s.connections[r.PathValue("id")]
	p, ok := s.provider(r.PathValue("provider"))
	if c == nil || c.token != "" || !ok {
		http.Error(w, "invalid connection or provider", http.StatusBadRequest)
		return
	}
	if len(s.logins) >= maxPending {
		http.Error(w, "too many logins", http.StatusTooManyRequests)
		return
	}
	state, verifier := randomToken(), randomToken()
	s.logins[state] = &login{connection: c.id, provider: p.ID, verifier: verifier, expires: c.expires}
	http.SetCookie(w, &http.Cookie{Name: "relay_login", Value: state, Path: "/auth/", HttpOnly: true, Secure: strings.HasPrefix(s.cfg.PublicURL, "https:"), SameSite: http.SameSiteLaxMode, MaxAge: int(connectTTL.Seconds())})
	q := url.Values{"client_id": {p.ClientID}, "redirect_uri": {s.callbackURL(p)}, "response_type": {"code"}, "state": {state}, "code_challenge": {digest(verifier)}, "code_challenge_method": {"S256"}}
	if p.Kind == "google" {
		q.Set("scope", "openid")
	}
	http.Redirect(w, r, s.endpoints[p.Kind].authorize+"?"+q.Encode(), http.StatusSeeOther)
}

func matchesCookie(r *http.Request, state string) bool {
	c, err := r.Cookie("relay_login")
	return err == nil && state != "" && subtle.ConstantTimeCompare([]byte(c.Value), []byte(state)) == 1
}

func (s *Service) callback(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	s.mu.Lock()
	s.cleanupLocked()
	l := s.logins[state]
	if l == nil || l.provider != r.PathValue("provider") || !matchesCookie(r, state) || r.URL.Query().Get("code") == "" {
		s.mu.Unlock()
		http.Error(w, "invalid or expired login", http.StatusBadRequest)
		return
	}
	// Consume the verifier before network I/O so concurrent callbacks cannot replay it.
	copyLogin := *l
	l.verifier = ""
	s.mu.Unlock()
	if copyLogin.verifier == "" {
		http.Error(w, "login already used", http.StatusBadRequest)
		return
	}
	p, _ := s.provider(copyLogin.provider)
	user, err := s.identity(r.Context(), p, copyLogin.verifier, r.URL.Query().Get("code"))
	if err != nil {
		s.mu.Lock()
		delete(s.logins, state)
		s.mu.Unlock()
		renderPage(w, pageData{ID: copyLogin.connection, Failed: true})
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.logins[state] != l || !s.cfg.Now().Before(l.expires) {
		http.Error(w, "login expired", http.StatusGone)
		return
	}
	c := s.connections[l.connection]
	if c == nil || c.token != "" || !s.cfg.Now().Before(c.expires) {
		http.Error(w, "connection expired or completed", http.StatusGone)
		return
	}
	token := randomToken()
	b, _ := json.Marshal(credential{User: digest(p.ID + ":" + user), ExpiresAt: s.cfg.Now().Add(credentialTTL).UnixMilli()})
	if err := s.cfg.Store.Put(r.Context(), credentialKey(token), b); err != nil {
		http.Error(w, "could not save enrollment", http.StatusInternalServerError)
		return
	}
	c.token = token
	delete(s.logins, state)
	http.SetCookie(w, &http.Cookie{Name: "relay_login", Path: "/auth/", MaxAge: -1, HttpOnly: true, Secure: strings.HasPrefix(s.cfg.PublicURL, "https:"), SameSite: http.SameSiteLaxMode})
	renderPage(w, pageData{})
}
