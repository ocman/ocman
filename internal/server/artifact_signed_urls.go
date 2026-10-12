package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const artifactURLTTL = 5 * time.Minute

// Separate the file signature from auth cookies, and bind it to the exact path.
func (a *Auth) artifactURLToken(path string, expires time.Time) string {
	exp := strconv.FormatInt(expires.Unix(), 10)
	mac := hmac.New(sha256.New, a.hmacKey)
	mac.Write([]byte("artifact-file\n" + path + "\n" + exp))
	return exp + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (a *Auth) validArtifactURLToken(path, token string, now time.Time) bool {
	expStr, _, ok := strings.Cut(token, ".")
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if !ok || err != nil || exp <= now.Unix() || exp > now.Add(artifactURLTTL).Unix() {
		return false
	}
	return hmac.Equal([]byte(token), []byte(a.artifactURLToken(path, time.Unix(exp, 0))))
}

// signArtifactFileURL makes an artifact file route absolute and, when auth
// is on, appends a short-lived token scoped to that one path.
func (s *Server) signArtifactFileURL(path string) string {
	if s.auth != nil {
		path += "?token=" + url.QueryEscape(s.auth.artifactURLToken(path, time.Now().Add(artifactURLTTL)))
	}
	return s.publicURL(path)
}

// Only the inert file route uses this wrapper. All other routes keep cookie auth.
func (s *Server) requireArtifactFileAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if s.auth != nil && r.Method == http.MethodGet && s.auth.validArtifactURLToken(r.URL.Path, r.URL.Query().Get("token"), time.Now()) {
			next(w, r)
			return
		}
		s.requireAuth(next)(w, r)
	}
}
