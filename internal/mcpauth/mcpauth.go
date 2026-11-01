// Package mcpauth owns the per-install bearer token that guards ocman's
// dedicated loopback MCP listener (/mcp and /hook). Any local user can reach
// a loopback port, so the peer address alone is not a credential.
//
// The token is generated once and sealed in state.db. A copy lives in a
// 0600 file in the ocman data directory so clients running as the same user
// can read it: OpenCode through a {file:...} reference in its config, Codex
// and Claude children through ocman's own supervisors, and `ocman hook`.
// The token never travels in a URL or on a command line.
package mcpauth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const (
	// SettingKey is the secret setting holding the token in state.db.
	SettingKey = "mcp_bearer_token"
	// FileName is the token file inside the ocman data directory.
	FileName = "mcp-token"
)

// SecretStore is the subset of state.DB that persists the token.
type SecretStore interface {
	GetSecretSetting(ctx context.Context, key string) (string, bool, error)
	SetSecretSetting(ctx context.Context, key, value string) error
}

// TokenPath is the token file for an ocman data directory.
func TokenPath(dataDir string) string { return filepath.Join(dataDir, FileName) }

// Ensure returns the install's token, generating and storing it on first use.
// When path is not empty it also (re)writes the token there with mode 0600,
// so a deleted or loosened file is repaired on every start.
func Ensure(ctx context.Context, store SecretStore, path string) (string, error) {
	if store == nil {
		return "", errors.New("state database unavailable")
	}
	token, ok, err := store.GetSecretSetting(ctx, SettingKey)
	if err != nil {
		return "", err
	}
	if !ok || token == "" {
		raw := make([]byte, 32)
		if _, err := rand.Read(raw); err != nil {
			return "", err
		}
		token = hex.EncodeToString(raw)
		if err := store.SetSecretSetting(ctx, SettingKey, token); err != nil {
			return "", err
		}
	}
	if path != "" {
		// No trailing newline, so config substitutions use it verbatim.
		if err := WritePrivateFile(path, []byte(token)); err != nil {
			return "", err
		}
	}
	return token, nil
}

// WritePrivateFile atomically replaces path with a 0600 file holding data,
// for the token and for client configs that embed it.
func WritePrivateFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(name) // no-op once renamed
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// ReadToken returns the token stored at path, or "" when it is unreadable.
func ReadToken(path string) string {
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path) //nolint:gosec // ocman's own token file
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// headerName is the request header carrying the token.
const headerName = "Authorization"

// Header is the Authorization header value for token.
func Header(token string) string { return "Bearer " + token }

// Headers is the header set an MCP client config sends for token.
func Headers(token string) map[string]string { return map[string]string{headerName: Header(token)} }

// SetHeader makes r carry token as its bearer credential.
func SetHeader(r *http.Request, token string) { r.Header.Set(headerName, Header(token)) }

// Authorized reports whether r carries token as its bearer credential. An
// empty token authorizes nothing.
func Authorized(r *http.Request, token string) bool {
	if token == "" {
		return false
	}
	scheme, got, ok := strings.Cut(r.Header.Get(headerName), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(strings.TrimSpace(got)), []byte(token)) == 1
}
