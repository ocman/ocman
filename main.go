package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	claudecodeplatform "github.com/NoUseFreak/ocman/internal/platforms/claudecode"
	opencodeplatform "github.com/NoUseFreak/ocman/internal/platforms/opencode"
	"github.com/NoUseFreak/ocman/internal/pricing"
	"github.com/NoUseFreak/ocman/internal/server"
	"github.com/NoUseFreak/ocman/internal/state"
)

// authPasswordEnv is the environment variable consulted for the auth
// password when neither -auth-password-file nor -auth-password is set.
// Env wins because it's the most common deployment channel (launchd,
// systemd, Docker) and keeps the secret out of shell history.
const authPasswordEnv = "OCMAN_AUTH_PASSWORD"

// authTrustLocalhostEnv opts localhost out of auth. The env var is
// treated truthy for "1", "true", "yes", "on" (case-insensitive);
// anything else (including empty) is false.
const authTrustLocalhostEnv = "OCMAN_AUTH_TRUST_LOCALHOST"

// knownPlatforms lists the valid values for the -platforms flag.
var knownPlatforms = map[string]bool{
	string(opencodeplatform.PlatformID):   true,
	string(claudecodeplatform.PlatformID): true,
}

func main() {
	addr := flag.String("addr", "127.0.0.1:8228", "listen address")
	dbPath := flag.String("db", db.DefaultDBPath(), "path to opencode.db")
	platformsFlag := flag.String("platforms", "opencode", "comma-separated list of platforms to enable (opencode, claude-code)")
	authPassword := flag.String("auth-password", "", "password required to access ocman (prefer "+authPasswordEnv+" env or -auth-password-file)")
	authPasswordFile := flag.String("auth-password-file", "", "read auth password from file (trimmed of trailing whitespace)")
	authSessionTTL := flag.Duration("auth-session-ttl", 30*24*time.Hour, "auth cookie lifetime")
	authTrustLocalhost := flag.Bool("auth-trust-localhost", false, "exempt loopback clients from auth (dev-mode escape hatch; also OCMAN_AUTH_TRUST_LOCALHOST)")
	flag.Parse()

	// Parse and validate the -platforms flag.
	enabledPlatforms := map[string]bool{}
	for _, name := range strings.Split(*platformsFlag, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if !knownPlatforms[name] {
			fmt.Fprintf(os.Stderr, "Unknown platform: %q (known: opencode, claude-code)\n", name)
			os.Exit(1)
		}
		enabledPlatforms[name] = true
	}
	if len(enabledPlatforms) == 0 {
		fmt.Fprintf(os.Stderr, "No platforms enabled. Use -platforms with at least one of: opencode, claude-code\n")
		os.Exit(1)
	}

	// Open the OpenCode database only when the opencode platform is enabled.
	var database *db.DB
	if enabledPlatforms[string(opencodeplatform.PlatformID)] {
		if _, err := os.Stat(*dbPath); os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "OpenCode database not found at: %s\n", *dbPath)
			fmt.Fprintf(os.Stderr, "Make sure OpenCode is installed and has been used at least once.\n")
			os.Exit(1)
		}

		var err error
		database, err = db.Open(*dbPath)
		if err != nil {
			log.Fatalf("Failed to open database: %v", err)
		}
		defer database.Close()
	}

	stateDB, err := state.Open(state.DefaultDBPath())
	if err != nil {
		log.Fatalf("Failed to open state database: %v", err)
	}
	defer stateDB.Close()

	// Pre-warm the pricing table in the background so the first metrics request
	// doesn't block on a remote fetch.
	go pricing.Load()

	// Create a context that is cancelled on SIGINT or SIGTERM.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Register only the platform adapters requested via -platforms.
	registry := platforms.NewRegistry()
	if enabledPlatforms[string(opencodeplatform.PlatformID)] {
		registry.Register(opencodeplatform.New(database, stateDB))
	}
	if enabledPlatforms[string(claudecodeplatform.PlatformID)] {
		registry.Register(claudecodeplatform.New())
	}

	auth, err := buildAuth(stateDB, *authPassword, *authPasswordFile, *authSessionTTL, *addr, *authTrustLocalhost)
	if err != nil {
		log.Fatalf("Failed to configure auth: %v", err)
	}

	srv := server.New(database, stateDB, *addr, registry, auth)
	if err := srv.Start(ctx); err != nil {
		log.Fatalf("Server error: %v", err)
	}

	log.Info("server stopped gracefully")
}

// resolveAuthPassword picks the password from env > file > flag, in
// that order. Returns "" if no source provided a value. A set but
// empty env var is treated as unset (so `OCMAN_AUTH_PASSWORD=` doesn't
// accidentally disable an explicit flag).
func resolveAuthPassword(flagValue, fileValue string) (string, error) {
	if v := strings.TrimRight(os.Getenv(authPasswordEnv), "\r\n"); v != "" {
		return v, nil
	}
	if fileValue != "" {
		b, err := os.ReadFile(fileValue)
		if err != nil {
			return "", fmt.Errorf("reading %s: %w", fileValue, err)
		}
		// Trim trailing newlines so `echo secret > passwd` works as
		// expected. Keep leading whitespace in case a password
		// legitimately starts with one (unlikely, but harmless).
		return strings.TrimRight(string(b), " \t\r\n"), nil
	}
	return flagValue, nil
}

// buildAuth assembles the server-side auth subsystem from the
// resolved config. Returns (nil, nil) when no password is configured
// — the server then runs in its pre-auth, open-by-default mode.
func buildAuth(stateDB *state.DB, flagValue, fileValue string, ttl time.Duration, addr string, trustLocalhostFlag bool) (*server.Auth, error) {
	password, err := resolveAuthPassword(flagValue, fileValue)
	if err != nil {
		return nil, err
	}
	if password == "" {
		return nil, nil
	}

	if flagValue != "" && os.Getenv(authPasswordEnv) == "" && fileValue == "" {
		// -auth-password leaks the secret into `ps` output; nudge
		// the operator toward env or file. Only warn; don't fail.
		log.Warn("-auth-password exposes the password to other local users via ps; " +
			"prefer " + authPasswordEnv + " or -auth-password-file")
	}

	hash, err := server.HashPassword(password)
	if err != nil {
		return nil, fmt.Errorf("hashing auth password: %w", err)
	}

	// Reuse the persisted HMAC key when present so cookies survive
	// restarts. On a fresh install (or after an explicit rotation)
	// generate and persist a new one.
	key, err := stateDB.AuthSecret()
	if err != nil {
		return nil, fmt.Errorf("loading auth secret: %w", err)
	}
	if len(key) == 0 {
		key, err = server.GenerateHMACKey()
		if err != nil {
			return nil, err
		}
		if err := stateDB.SetAuthSecret(key); err != nil {
			return nil, fmt.Errorf("persisting auth secret: %w", err)
		}
	}

	trustLocalhost := trustLocalhostFlag || parseBoolEnv(os.Getenv(authTrustLocalhostEnv))

	if !trustLocalhost && isLoopbackAddr(addr) {
		// Configured, listener is loopback-only, and localhost is
		// NOT trusted: the dashboard is effectively unreachable
		// without logging in from the same browser as the operator.
		// Still valid (and the intended posture), but worth naming.
		log.WithField("addr", addr).Info("auth required for all clients (incl. localhost) and -addr is loopback-only")
	}

	return server.NewAuth(server.AuthConfig{
		PasswordHash:   hash,
		HMACKey:        key,
		CookieTTL:      ttl,
		TrustLocalhost: trustLocalhost,
	})
}

// parseBoolEnv returns true for the common truthy spellings. Empty
// is false so unsetting the env var returns the default behaviour.
func parseBoolEnv(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// isLoopbackAddr returns true when the listener's host part is a
// loopback literal. Pure-string check because the listener hasn't
// opened yet at this point.
func isLoopbackAddr(addr string) bool {
	host := addr
	if idx := strings.LastIndex(host, ":"); idx >= 0 {
		host = host[:idx]
	}
	host = strings.TrimPrefix(host, "[")
	host = strings.TrimSuffix(host, "]")
	return host == "" || host == "127.0.0.1" || host == "::1" || host == "localhost"
}
