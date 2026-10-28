package filesystem

import (
	"context"
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

	"github.com/ocman/ocman/internal/httpx"
	"github.com/ocman/ocman/internal/state"
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
func loadFileTokenSecret(ctx context.Context, sdb *state.DB) ([]byte, error) {
	if sdb == nil {
		return nil, errors.New("state database unavailable")
	}
	stored, ok, err := sdb.GetSetting(ctx, fileTokenSecretKey)
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
	_, _ = rand.Read(key)
	if err := sdb.SetSetting(ctx, fileTokenSecretKey, base64.RawStdEncoding.EncodeToString(key)); err != nil {
		return nil, err
	}
	return key, nil
}

// fileTokenSecret memoises a successful load for the process lifetime; a
// failed one (a canceled request's context) is retried by the next caller.
func (m *Module) fileTokenSecret(ctx context.Context) ([]byte, error) {
	m.keyMu.Lock()
	defer m.keyMu.Unlock()
	if m.key == nil {
		key, err := loadFileTokenSecret(ctx, m.Store)
		if err != nil {
			return nil, err
		}
		m.key = key
	}
	return m.key, nil
}

// FileURL returns an origin-relative URL that serves the file at absPath.
// Backs the MCP embed_file tool, so an agent can hand the user a viewable
// link to an asset it generated on disk.
func (m *Module) FileURL(ctx context.Context, absPath string) (string, error) {
	key, err := m.fileTokenSecret(ctx)
	if err != nil {
		return "", err
	}
	return filePathPrefix + signFilePath(key, absPath), nil
}

// handleFileProxy serves GET /api/file/{token}: the bytes of a file an
// agent explicitly embedded. The signature is the authorisation — an
// unsigned or altered path is rejected — on top of the dashboard auth guard.
func (m *Module) handleFileProxy(w http.ResponseWriter, r *http.Request) {
	key, err := m.fileTokenSecret(r.Context())
	if err != nil {
		httpx.ServerError(w, "loading file token secret", err)
		return
	}
	path, ok := verifyFileToken(key, strings.TrimPrefix(r.URL.Path, filePathPrefix))
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
	httpx.SetInertFileHeaders(w)
	w.Header().Set("Content-Disposition", fmt.Sprintf("%s; filename=%q", httpx.FileDisposition(ctype), filepath.Base(path)))
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
