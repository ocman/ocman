package relayauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
)

func randomToken() string { return base64.RawURLEncoding.EncodeToString(randomBytes()) }
func randomBytes() []byte { b := make([]byte, 32); _, _ = rand.Read(b); return b }
func digest(v string) string {
	h := sha256.Sum256([]byte(v))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

type credential struct {
	User      string `json:"user"`
	ExpiresAt int64  `json:"expiresAt"`
}

func credentialKey(token string) string { return "enrollment/" + digest(token) + "/credential" }

// Authorize accepts only a persisted, unexpired credential. Storage failures
// fail closed. Tokens are opaque and only their hashes are stored on the relay.
func (s *Service) Authorize(ctx context.Context, token string) bool {
	if token == "" {
		return false
	}
	b, err := s.cfg.Store.Get(ctx, credentialKey(token))
	var c credential
	return err == nil && json.Unmarshal(b, &c) == nil && c.User != "" && s.cfg.Now().UnixMilli() < c.ExpiresAt
}

func bearer(r *http.Request) string {
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		return ""
	}
	return strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
}

func (s *Service) revoke(w http.ResponseWriter, r *http.Request) {
	token := bearer(r)
	if !s.Authorize(r.Context(), token) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if err := s.cfg.Store.DeletePrefix(r.Context(), "enrollment/"+digest(token)+"/"); err != nil {
		http.Error(w, "could not revoke enrollment", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func sendJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
