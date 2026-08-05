package server

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/NoUseFreak/ocman/internal/state"
)

// fileTokenSecretKey is the `setting` row holding the HMAC key used to
// sign embedded-file tokens. Separate from auth_secret so rotating the
// login password does not invalidate every embedded asset.
const fileTokenSecretKey = "file_token_secret"

// filePathPrefix is the route the signed tokens are served under.
const filePathPrefix = "/api/file/"

// signFilePath mints an opaque, tamper-proof handle for an absolute
// path: "<base64(path)>.<base64(hmac)>". Stateless — no registry of
// embedded files to store, expire, or clean up.
//
// ponytail: no expiry. An embedded asset is referenced from a conversation
// that stays readable forever; an expiring link would just rot in the
// transcript. Add a timestamp to the signed payload if links ever need
// to age out.
func signFilePath(key []byte, path string) string {
	enc := base64.RawURLEncoding.EncodeToString([]byte(path))
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(path))
	return enc + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// verifyFileToken returns the absolute path a token was minted for, or
// false when the token is malformed or not signed with key.
func verifyFileToken(key []byte, token string) (string, bool) {
	enc, sig, ok := strings.Cut(token, ".")
	if !ok {
		return "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(enc)
	if err != nil {
		return "", false
	}
	want, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil {
		return "", false
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(raw)
	if !hmac.Equal(mac.Sum(nil), want) {
		return "", false
	}
	return string(raw), true
}

// loadFileTokenSecret returns the persisted signing key, generating and
// storing one on first use.
func loadFileTokenSecret(sdb *state.DB) ([]byte, error) {
	if sdb == nil {
		return nil, errors.New("state database unavailable")
	}
	stored, ok, err := sdb.GetSetting(fileTokenSecretKey)
	if err != nil {
		return nil, err
	}
	if ok && stored != "" {
		key, err := base64.RawStdEncoding.DecodeString(stored)
		if err == nil && len(key) >= 16 {
			return key, nil
		}
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generating file token secret: %w", err)
	}
	if err := sdb.SetSetting(fileTokenSecretKey, base64.RawStdEncoding.EncodeToString(key)); err != nil {
		return nil, err
	}
	return key, nil
}

// fileTokenSecret memoises loadFileTokenSecret for the process lifetime.
func (s *Server) fileTokenSecret() ([]byte, error) {
	s.fileKeyOnce.Do(func() {
		s.fileKey, s.fileKeyErr = loadFileTokenSecret(s.stateDB)
	})
	return s.fileKey, s.fileKeyErr
}

// FileURL returns an absolute, browser-reachable URL that serves the file at
// absPath. Used by the MCP embed_file tool so an agent can hand the
// user a viewable link to an asset it generated on disk.
func (s *Server) FileURL(absPath string) (string, error) {
	key, err := s.fileTokenSecret()
	if err != nil {
		return "", err
	}
	base := s.publicBaseURL
	if base == "" {
		addr := s.addr
		if addr == "" {
			addr = "localhost:8228"
		}
		if strings.HasPrefix(addr, ":") {
			addr = "localhost" + addr
		}
		base = "http://" + addr
	}
	return strings.TrimRight(base, "/") + filePathPrefix + signFilePath(key, absPath), nil
}

// handleFileProxy serves GET /api/file/{token}: the bytes of a file an
// agent explicitly embedded via the MCP embed_file tool. The signature is
// the authorisation — an unsigned or altered path is rejected — on top
// of the normal dashboard auth guard.
func (s *Server) handleFileProxy(w http.ResponseWriter, r *http.Request) {
	key, err := s.fileTokenSecret()
	if err != nil {
		serverError(w, "loading file token secret", err)
		return
	}
	token := strings.TrimPrefix(r.URL.Path, filePathPrefix)
	path, ok := verifyFileToken(key, token)
	if !ok {
		http.Error(w, "invalid file token", http.StatusForbidden)
		return
	}

	f, err := os.Open(path)
	if err != nil {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.Error(w, "not a regular file", http.StatusNotFound)
		return
	}

	ctype := fileContentType(path)
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// Neutralise active content (SVG and HTML can carry script) when the
	// asset is opened as a top-level document.
	w.Header().Set("Content-Security-Policy", "sandbox")
	w.Header().Set("Content-Disposition", fmt.Sprintf("%s; filename=%q", fileDisposition(ctype), filepath.Base(path)))
	http.ServeContent(w, r, path, info.ModTime(), f)
}

// fileContentType maps an extension to a MIME type, defaulting to a
// generic binary type when the extension is unknown.
func fileContentType(path string) string {
	if ct := mime.TypeByExtension(filepath.Ext(path)); ct != "" {
		return ct
	}
	return "application/octet-stream"
}

// fileDisposition reports whether a content type is one browsers render
// usefully in place ("inline") or should just download ("attachment").
func fileDisposition(ctype string) string {
	base, _, _ := strings.Cut(ctype, ";")
	base = strings.TrimSpace(base)
	switch {
	case strings.HasPrefix(base, "image/"),
		strings.HasPrefix(base, "video/"),
		strings.HasPrefix(base, "audio/"),
		strings.HasPrefix(base, "text/"),
		base == "application/pdf",
		base == "application/json":
		return "inline"
	}
	return "attachment"
}
