package app

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/NoUseFreak/ocman/internal/server"
	"github.com/NoUseFreak/ocman/internal/state"
	log "github.com/sirupsen/logrus"
)

// Authentication environment variables take precedence over command-line flags.
const (
	authPasswordEnv           = "OCMAN_AUTH_PASSWORD"
	opencodeServerPasswordEnv = "OPENCODE_SERVER_PASSWORD"
	authTrustLocalhostEnv     = "OCMAN_AUTH_TRUST_LOCALHOST"
	publicBaseURLEnv          = "OCMAN_PUBLIC_BASE_URL"
	insecureNoAuthEnv         = "OCMAN_INSECURE_NO_AUTH"
)

// resolveOpenCodePassword uses env > file > generated > disabled.
func resolveOpenCodePassword(fileValue string, generate bool) (string, error) {
	if value := strings.TrimRight(os.Getenv(opencodeServerPasswordEnv), "\r\n"); value != "" {
		return value, nil
	}
	if fileValue != "" {
		b, err := os.ReadFile(fileValue)
		if err != nil {
			return "", fmt.Errorf("reading %s: %w", fileValue, err)
		}
		return strings.TrimRight(string(b), " \t\r\n"), nil
	}
	if !generate {
		return "", nil
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating OpenCode server password: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// validateListenExposure refuses a startup that would expose ocman's
// session-control API to anyone who can reach it. Only loopback binds are
// unauthenticated-safe; everything else needs a password or an opt-out.
//
// A public -public-base-url counts as exposure even on a loopback bind: it
// documents a reverse proxy in front of ocman, and every proxied request
// arrives from 127.0.0.1, so the loopback peer check is no credential.
func validateListenExposure(addr, publicBaseURL string, authConfigured, allowInsecure bool) error {
	if authConfigured || allowInsecure {
		return nil
	}
	if !isLoopbackAddr(addr) {
		return fmt.Errorf("refusing to listen on %s with no password configured: "+
			"anyone who can reach that address could drive your coding agents. "+
			"Set %s (or -auth-password-file), bind to 127.0.0.1, or pass -insecure-no-auth (%s=1) to override",
			addr, authPasswordEnv, insecureNoAuthEnv)
	}
	if publicBaseURL != "" && !baseURLIsLoopback(publicBaseURL) {
		return fmt.Errorf("refusing to run with -public-base-url %q and no password configured: "+
			"behind a reverse proxy every request looks local, so anyone who can reach that URL "+
			"could drive your coding agents. "+
			"Set %s (or -auth-password-file), or pass -insecure-no-auth (%s=1) to override",
			publicBaseURL, authPasswordEnv, insecureNoAuthEnv)
	}
	return nil
}

// baseURLIsLoopback reports whether a public base URL names a loopback
// host. The parsed hostname is classified on its own: the listener
// helper's last-colon port split would corrupt bracketed IPv6 literals.
// An unparseable or host-less value is non-loopback: checks fail closed.
func baseURLIsLoopback(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// resolveAuthPassword uses env > file > flag, preserving leading whitespace.
func resolveAuthPassword(flagValue, fileValue string) (string, error) {
	if v := strings.TrimRight(os.Getenv(authPasswordEnv), "\r\n"); v != "" {
		return v, nil
	}
	if fileValue != "" {
		b, err := os.ReadFile(fileValue)
		if err != nil {
			return "", fmt.Errorf("reading %s: %w", fileValue, err)
		}
		return strings.TrimRight(string(b), " \t\r\n"), nil
	}
	return flagValue, nil
}

func buildAuth(ctx context.Context, stateDB *state.DB, flagValue, fileValue string, ttl time.Duration, addr string, trustLocalhostFlag bool) (*server.Auth, error) {
	password, err := resolveAuthPassword(flagValue, fileValue)
	if err != nil {
		return nil, err
	}
	if password == "" {
		return nil, nil
	}
	if flagValue != "" && os.Getenv(authPasswordEnv) == "" && fileValue == "" {
		log.Warn("-auth-password exposes the password to other local users via ps; " + "prefer " + authPasswordEnv + " or -auth-password-file")
	}
	hash, err := server.HashPassword(password)
	if err != nil {
		return nil, fmt.Errorf("hashing auth password: %w", err)
	}
	// Reuse the persisted HMAC key so cookies survive restarts.
	key, err := stateDB.AuthSecret(ctx)
	if err != nil {
		return nil, fmt.Errorf("loading auth secret: %w", err)
	}
	if len(key) == 0 {
		key, err = server.GenerateHMACKey()
		if err != nil {
			return nil, err
		}
		if err := stateDB.SetAuthSecret(ctx, key); err != nil {
			return nil, fmt.Errorf("persisting auth secret: %w", err)
		}
	}
	trustLocalhost := trustLocalhostFlag || parseBoolEnv(os.Getenv(authTrustLocalhostEnv))
	if !trustLocalhost && isLoopbackAddr(addr) {
		log.WithField("addr", addr).Info("auth required for all clients (incl. localhost) and -addr is loopback-only")
	}
	return server.NewAuth(server.AuthConfig{PasswordHash: hash, HMACKey: key, CookieTTL: ttl, TrustLocalhost: trustLocalhost})
}

func parseBoolEnv(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func isAppBundle() bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	return strings.Contains(exe, ".app/Contents/MacOS/")
}

func isLoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return host == "localhost" || ip != nil && ip.IsLoopback()
}
