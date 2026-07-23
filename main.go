package main

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/gui"
	"github.com/NoUseFreak/ocman/internal/ocapi"
	"github.com/NoUseFreak/ocman/internal/opencodeskills"
	"github.com/NoUseFreak/ocman/internal/platforms"
	opencodeplatform "github.com/NoUseFreak/ocman/internal/platforms/opencode"
	"github.com/NoUseFreak/ocman/internal/pricing"
	"github.com/NoUseFreak/ocman/internal/remote"
	"github.com/NoUseFreak/ocman/internal/server"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/telemetry"
)

// version is overridden at build time via -ldflags='-X main.version=...'.
// It's surfaced as service.version on every OTel resource.
var version = "dev"

//go:embed .opencode/skills/ocman-sessions/SKILL.md
var sessionSplittingSkill []byte

//go:embed .opencode/skills/ocman-workflows/SKILL.md
var workflowsSkill []byte

// authPasswordEnv is the environment variable consulted for the auth
// password when neither -auth-password-file nor -auth-password is set.
// Env wins because it's the most common deployment channel (launchd,
// systemd, Docker) and keeps the secret out of shell history.
const authPasswordEnv = "OCMAN_AUTH_PASSWORD"

const opencodeServerPasswordEnv = "OPENCODE_SERVER_PASSWORD"

// authTrustLocalhostEnv opts localhost out of auth. The env var is
// treated truthy for "1", "true", "yes", "on" (case-insensitive);
// anything else (including empty) is false.
const authTrustLocalhostEnv = "OCMAN_AUTH_TRUST_LOCALHOST"

// publicBaseURLEnv supplies the externally reachable base URL used to
// build absolute share links. Set this when ocman runs behind a reverse
// proxy on a stable hostname (e.g. "https://ocman.example.com"). When
// unset, share links derive their origin from the incoming request's
// scheme + Host header, which is correct for localhost / dev.
const publicBaseURLEnv = "OCMAN_PUBLIC_BASE_URL"

// knownPlatforms lists the valid values for the -platforms flag.
var knownPlatforms = map[string]bool{
	string(opencodeplatform.PlatformID): true,
}

func main() {
	// Wails's binding generator runs the binary with WAILS_BINDINGS set to
	// collect exported method signatures.  We have no bound methods, so
	// there is nothing to do — exit cleanly so the generator doesn't
	// mistake a server startup error for a binding failure.
	if os.Getenv("WAILS_BINDINGS") != "" {
		os.Exit(0)
	}

	// Colored text logs. logrus already defaults to text, but disables
	// color when stdout isn't a TTY — which it isn't under `make dev`/air
	// (piped). ForceColors keeps the color; FullTimestamp adds the date.
	log.SetFormatter(&log.TextFormatter{ForceColors: true, FullTimestamp: true})
	if err := opencodeskills.Install(map[string][]byte{
		"ocman-sessions": sessionSplittingSkill,
		"ocman-workflows":         workflowsSkill,
	}); err != nil {
		log.WithError(err).Warn("installing embedded ocman skills")
	}

	// When started by launchd / a login item after a reboot, ocman
	// inherits a minimal PATH that omits homebrew and version-manager
	// shims, so tmux/opencode/git look unavailable. Recover the login
	// shell's PATH before any exec.LookPath runs.
	ensureToolPath()

	addr := flag.String("addr", "127.0.0.1:8228", "listen address")
	guiMode := flag.Bool("gui", isAppBundle(), "open a native desktop window (Wails) instead of just serving HTTP")
	guiAddr := flag.String("gui-addr", "127.0.0.1:0", "listen address for the backend when --gui is set (default picks an ephemeral port)")
	dbPath := flag.String("db", db.DefaultDBPath(), "path to opencode.db")
	platformsFlag := flag.String("platforms", "opencode", "comma-separated list of platforms to enable (opencode)")
	authPassword := flag.String("auth-password", "", "password required to access ocman (prefer "+authPasswordEnv+" env or -auth-password-file)")
	authPasswordFile := flag.String("auth-password-file", "", "read auth password from file (trimmed of trailing whitespace)")
	authSessionTTL := flag.Duration("auth-session-ttl", 30*24*time.Hour, "auth cookie lifetime")
	authTrustLocalhost := flag.Bool("auth-trust-localhost", false, "exempt loopback clients from auth (dev-mode escape hatch; also OCMAN_AUTH_TRUST_LOCALHOST)")
	opencodePasswordFile := flag.String("opencode-server-password-file", "", "read the managed OpenCode API password from a file")
	opencodeGeneratePassword := flag.Bool("opencode-server-generate-password", false, "generate an ephemeral managed OpenCode API password at startup")
	otelEndpoint := flag.String("otel", "", "OTLP endpoint URL (e.g. http://localhost:4318 or grpc://localhost:4317). Empty disables telemetry. Falls back to OTEL_EXPORTER_OTLP_ENDPOINT.")
	autoApprove := flag.Bool("auto-approve", false, "default new sessions to auto-approve mode (uses OpenCode's running instance as the LLM judge)")
	publicBaseURL := flag.String("public-base-url", "", "externally reachable base URL for share links (e.g. https://ocman.example.com); falls back to "+publicBaseURLEnv+" env, then the request Host")
	remoteListen := flag.String("remote-listen", "", "bind address for the remote-access gRPC server (e.g. 0.0.0.0:8230); empty disables it (multi-remote support)")
	remoteTLSCert := flag.String("remote-tls-cert", "", "TLS certificate file for the remote-access gRPC server (enables TLS together with -remote-tls-key)")
	remoteTLSKey := flag.String("remote-tls-key", "", "TLS key file for the remote-access gRPC server")
	remoteTrustedOverlay := flag.Bool("remote-trusted-overlay", false, "explicitly allow plaintext remote gRPC on a trusted overlay network")
	flag.Parse()
	if err := validateRemoteTransport(*remoteListen, *remoteTLSCert, *remoteTLSKey, *remoteTrustedOverlay); err != nil {
		log.Fatal(err)
	}

	// Resolve the public base URL: flag wins, then env. Empty leaves
	// the "derive from request Host" behaviour in the server.
	resolvedBaseURL := *publicBaseURL
	if resolvedBaseURL == "" {
		resolvedBaseURL = strings.TrimSpace(os.Getenv(publicBaseURLEnv))
	}

	// Parse and validate the -platforms flag.
	enabledPlatforms := map[string]bool{}
	for _, name := range strings.Split(*platformsFlag, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if !knownPlatforms[name] {
			fmt.Fprintf(os.Stderr, "Unknown platform: %q (known: opencode)\n", name)
			os.Exit(1)
		}
		enabledPlatforms[name] = true
	}
	if len(enabledPlatforms) == 0 {
		fmt.Fprintf(os.Stderr, "No platforms enabled. Use -platforms with at least one of: opencode\n")
		os.Exit(1)
	}

	// Create a context that is cancelled on SIGINT or SIGTERM. Built
	// up-front so telemetry init and DB open can use it for cancellation.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Initialise OpenTelemetry as early as possible so DB / HTTP client
	// instrumentation registered later picks up the global providers.
	// When --otel is empty (and OTEL_EXPORTER_OTLP_ENDPOINT is unset)
	// this returns a no-op shutdown and leaves the SDK no-op providers
	// in place — every otel call site stays cheap.
	//
	// The shutdown is deferred *here* (before the DB defers below) so
	// LIFO ordering flushes spans/metrics first, then closes DBs.
	shutdownTel, err := telemetry.Init(ctx, *otelEndpoint, version)
	if err != nil {
		log.Fatalf("Failed to initialise telemetry: %v", err)
	}
	defer func() {
		// Use a fresh context with timeout: the main ctx is
		// already cancelled by the time defers run.
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdownTel(sctx); err != nil {
			log.WithError(err).Warn("OTel shutdown error")
		}
	}()

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

	opencodePassword, err := resolveOpenCodePassword(*opencodePasswordFile, *opencodeGeneratePassword)
	if err != nil {
		log.Fatalf("Failed to configure OpenCode server auth: %v", err)
	}
	opencodeAuth := ocapi.New(opencodePassword)

	// Ensure this instance has a stable random identity + remote-access
	// token (multi-remote support). Generated and persisted on first
	// startup; reused thereafter. No networking is started here — the
	// gRPC remote-listen server is opt-in via -remote-listen.
	ident, err := stateDB.InstanceIdentity()
	if err != nil {
		log.Fatalf("Failed to ensure instance identity: %v", err)
	}

	// Pre-warm the pricing table in the background so the first metrics request
	// doesn't block on a remote fetch.
	go pricing.Load()

	// Register only the platform adapters requested via -platforms.
	// The OpenCode adapter takes the pricing table so SessionInfo can
	// estimate cost for sessions whose upstream `cost` field is zero
	// (subscription-plan accounts). pricing.Load() is async-safe — it
	// returns the same Table even if the background fetch is still
	// running; calls into it just see an empty table until then.
	registry := platforms.NewRegistry()
	if enabledPlatforms[string(opencodeplatform.PlatformID)] {
		registry.Register(opencodeplatform.NewWithPricingAndAuth(database, stateDB, pricing.Load(), opencodeAuth))
		// Keep the unfiltered sessions cache warm so /api/sessions and
		// notify polls read from memory instead of blocking ~5s on the
		// GetSessions query (which has heavy per-session subqueries).
		if database != nil {
			opencodeplatform.StartSessionsRefresher(ctx, database)
		}
	}
	auth, err := buildAuth(stateDB, *authPassword, *authPasswordFile, *authSessionTTL, *addr, *authTrustLocalhost)
	if err != nil {
		log.Fatalf("Failed to configure auth: %v", err)
	}

	// In GUI mode the server listens on an ephemeral loopback port that
	// is only reachable from the Wails WebView proxy.  In headless mode
	// it listens on *addr as before.
	if *guiMode {
		// gui-addr defaults to 127.0.0.1:0 (ephemeral). Callers can
		// pin a port with --gui-addr=127.0.0.1:8229 if needed.
		listenAddr := *guiAddr
		srv := server.New(database, stateDB, listenAddr, registry, auth).
			WithOpenCodeAuth(opencodeAuth).
			WithAutoApproveDefault(*autoApprove).
			WithPublicBaseURL(resolvedBaseURL).
			WithRemoteAccess(ident.InstanceID, "", false, false)
		if err := gui.RunGUI(ctx, srv, listenAddr); err != nil {
			log.Fatalf("GUI error: %v", err)
		}
	} else {
		srv := server.New(database, stateDB, *addr, registry, auth).
			WithOpenCodeAuth(opencodeAuth).
			WithAutoApproveDefault(*autoApprove).
			WithPublicBaseURL(resolvedBaseURL)

		// Start the remote-access gRPC server when -remote-listen is set
		// (multi-remote support). Off by default so single-host installs
		// are byte-for-byte unchanged (NFR-6).
		listening, listenAddr, tlsOn := startRemoteServer(ctx, srv, ident, *remoteListen, *remoteTLSCert, *remoteTLSKey, *remoteTrustedOverlay)
		srv.WithRemoteAccess(ident.InstanceID, listenAddr, listening, tlsOn)

		// Start the hub-side remote manager: it loads any saved remotes
		// from state.db and dials them in the background, registering
		// remote platform/host adapters as they connect. With zero saved
		// remotes this is a no-op (NFR-6).
		mgr := remote.NewManager(srv.Registry(), srv.HostRouter(), stateDB, string(opencodeplatform.PlatformID))
		mgr.Start(ctx)
		srv.SetRemoteManager(mgr)
		defer mgr.Stop()
		// Periodically refresh per-remote project inventories so the
		// machine picker matches against fresh data (AD-8).
		go mgr.RunInventoryLoop(ctx, 5*time.Minute)

		if err := srv.Start(ctx); err != nil {
			log.Fatalf("Server error: %v", err)
		}
	}

	log.Info("server stopped gracefully")
}

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

func validateRemoteTransport(listenAddr, tlsCert, tlsKey string, trustedOverlay bool) error {
	if listenAddr == "" {
		return nil
	}
	if (tlsCert == "") != (tlsKey == "") {
		return fmt.Errorf("remote TLS certificate and key must be provided together")
	}
	if tlsCert == "" && !trustedOverlay {
		return fmt.Errorf("remote TLS is required unless -remote-trusted-overlay is set")
	}
	return nil
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

// startRemoteServer starts the remote-access gRPC server when listenAddr
// is non-empty. It returns (listening, boundAddr, tls). On failure it
// logs and returns listening=false so the HTTP server still starts —
// the remote surface is opt-in and must never block normal operation.
func startRemoteServer(ctx context.Context, srv *server.Server, ident state.InstanceIdentity, listenAddr, tlsCert, tlsKey string, trustedOverlay bool) (bool, string, bool) {
	if listenAddr == "" {
		return false, "", false
	}
	rsrv := remote.NewServer(srv.Registry(), srv.RemoteServerHost(), ident.InstanceID, version).
		UseSessions(srv.SessionService())
	ln, err := remote.NewListener(remote.ListenConfig{
		Addr:           listenAddr,
		Token:          ident.RemoteToken,
		TLSCertFile:    tlsCert,
		TLSKeyFile:     tlsKey,
		TrustedOverlay: trustedOverlay,
	}, rsrv)
	if err != nil {
		log.WithError(err).Error("remote: failed to start gRPC server; continuing without it")
		return false, "", false
	}
	if !ln.TLS() {
		log.WithField("addr", ln.Addr()).Info("remote: gRPC server using trusted-overlay plaintext transport")
	}
	go func() {
		if err := ln.Serve(); err != nil {
			log.WithError(err).Warn("remote: gRPC server stopped")
		}
	}()
	go func() {
		<-ctx.Done()
		ln.Stop()
	}()
	transport := "trusted-overlay"
	if ln.TLS() {
		transport = "tls"
	}
	log.WithFields(log.Fields{"addr": ln.Addr(), "transport": transport}).Info("remote: gRPC server listening")
	return true, ln.Addr(), ln.TLS()
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

// isAppBundle reports whether the running executable is inside a macOS .app
// bundle (i.e. its path contains ".app/Contents/MacOS/"). When true, --gui
// defaults to on so double-clicking the .app opens a window without needing
// any flags.
func isAppBundle() bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	return strings.Contains(exe, ".app/Contents/MacOS/")
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
