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
	"strconv"
	"strings"
	"time"

	"github.com/ocman/ocman/internal/httpx"
	"github.com/ocman/ocman/internal/state"
)

// fileTokenSecretKey is the `setting` row holding the HMAC key used to
// sign embedded-file tokens. Separate from auth_secret so rotating the
// login password does not invalidate every embedded asset.
const fileTokenSecretKey = "file_token_secret"

// filePathPrefix is the route the signed tokens are served under.
const filePathPrefix = "/api/file/"

// fileRefreshPath re-mints an embedded-file URL for the dashboard.
const fileRefreshPath = "/api/files/refresh"

// fileURLTTL bounds how long a minted file URL is served. Embedded files
// live in transcripts the user reopens days later, and exported or copied
// transcripts carry the URL elsewhere. The dashboard re-mints stale URLs
// through fileRefreshPath on render, so the TTL only has to outlive an open
// tab and a stretch of agent output read outside the dashboard; seven days
// keeps leaked copies short-lived without breaking those.
const fileURLTTL = 7 * 24 * time.Hour

// fileURLExpiry rounds expiries down to the day so every URL minted for a
// path on one day is identical: browser caches and image-gallery links stay
// stable across reloads. Lifetimes are therefore six to seven days.
func fileURLExpiry(now time.Time) int64 {
	return now.Add(fileURLTTL).Truncate(24 * time.Hour).Unix()
}

// fileTokenMAC binds the signature to the path and the expiry. The prefix
// keeps it disjoint from the legacy path-only MAC.
func fileTokenMAC(key []byte, path, exp string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("ocman-file\n" + exp + "\n" + path))
	return mac.Sum(nil)
}

// signFilePath mints an opaque, tamper-proof handle for an absolute path:
// "<base64(path)>.<unix expiry>.<base64(hmac)>". Stateless — no registry
// of embedded files to store or clean up.
func signFilePath(key []byte, path string, exp int64) string {
	e := strconv.FormatInt(exp, 10)
	return base64.RawURLEncoding.EncodeToString([]byte(path)) + "." + e + "." +
		base64.RawURLEncoding.EncodeToString(fileTokenMAC(key, path, e))
}

// parseFileToken returns the path a token was signed for and its expiry,
// ignoring whether it has passed. Legacy "<path>.<hmac>" tokens minted
// before expiries existed parse with exp 0. Only re-minting accepts those.
func parseFileToken(key []byte, token string) (path string, exp int64, ok bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 && len(parts) != 3 {
		return "", 0, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", 0, false
	}
	got, err := base64.RawURLEncoding.DecodeString(parts[len(parts)-1])
	if err != nil {
		return "", 0, false
	}
	if len(parts) == 2 {
		mac := hmac.New(sha256.New, key)
		mac.Write(raw)
		return string(raw), 0, hmac.Equal(mac.Sum(nil), got)
	}
	exp, err = strconv.ParseInt(parts[1], 10, 64)
	if err != nil || exp <= 0 || strconv.FormatInt(exp, 10) != parts[1] {
		return "", 0, false
	}
	if !hmac.Equal(fileTokenMAC(key, string(raw), parts[1]), got) {
		return "", 0, false
	}
	return string(raw), exp, true
}

// verifyFileToken returns the absolute path a token was minted for, or
// false when the token is malformed, not signed with key, has no expiry,
// has expired, or claims a lifetime longer than fileURLTTL.
func verifyFileToken(key []byte, token string, now time.Time) (string, bool) {
	path, exp, ok := parseFileToken(key, token)
	if !ok || exp <= now.Unix() || exp > now.Add(fileURLTTL).Unix() {
		return "", false
	}
	return path, true
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
	return filePathPrefix + signFilePath(key, absPath, fileURLExpiry(m.now())), nil
}

// handleFileURLRefresh serves POST /api/files/refresh: given a file URL this
// server signed, expired or legacy, it returns a fresh one for the same
// path. The dashboard calls it while rendering a transcript, so an embed
// keeps rendering after its URL ages out while copies of the old URL stop
// working on their own. It needs the same login as the proxy itself and
// cannot sign a path the server never signed.
func (m *Module) handleFileURLRefresh(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL string `json:"url"`
	}
	if !httpx.ReadJSON(w, r, 16<<10, &req) {
		return
	}
	key, err := m.fileTokenSecret(r.Context())
	if err != nil {
		httpx.ServerError(w, "loading file token secret", err)
		return
	}
	token, ok := strings.CutPrefix(req.URL, filePathPrefix)
	if !ok {
		http.Error(w, "invalid file token", http.StatusForbidden)
		return
	}
	path, _, ok := parseFileToken(key, token)
	if !ok {
		http.Error(w, "invalid file token", http.StatusForbidden)
		return
	}
	httpx.WriteJSON(w, map[string]string{"url": filePathPrefix + signFilePath(key, path, fileURLExpiry(m.now()))})
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
	path, ok := verifyFileToken(key, strings.TrimPrefix(r.URL.Path, filePathPrefix), m.now())
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
