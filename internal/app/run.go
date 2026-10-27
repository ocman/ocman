// Package app composes ocman's platforms, databases, servers and desktop shell.
package app

import (
	"context"
	"flag"
	"net"
	"os"
	"os/signal"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/gui"
	"github.com/NoUseFreak/ocman/internal/opencodeskills"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/claude/transport"
	codextransport "github.com/NoUseFreak/ocman/internal/platforms/codex/transport"
	"github.com/NoUseFreak/ocman/internal/platforms/opencode/api"
	opencodeplatform "github.com/NoUseFreak/ocman/internal/platforms/opencode/provider"
	"github.com/NoUseFreak/ocman/internal/platformsettings"
	"github.com/NoUseFreak/ocman/internal/pricing"
	"github.com/NoUseFreak/ocman/internal/providerprotocol"
	"github.com/NoUseFreak/ocman/internal/remote"
	"github.com/NoUseFreak/ocman/internal/server"
	"github.com/NoUseFreak/ocman/internal/share"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/telemetry"
	"github.com/NoUseFreak/ocman/internal/toolpath"
)

var version = "dev"
var skillCatalog map[string][]byte

func embeddedSkills() map[string][]byte {
	return skillCatalog
}

// Run starts ocman with the build version and skills embedded by its entrypoint.
func Run(buildVersion string, skills map[string][]byte) {
	version, skillCatalog = buildVersion, skills
	run()
}

func run() {
	if codextransport.RunProviderCommand(os.Args[1:]) || claudecode.RunProviderCommand(os.Args[1:]) || claudecode.RunHookCommand(os.Args[1:], os.Stdin) {
		return
	}
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

	// When started by launchd / a login item after a reboot, ocman
	// inherits a minimal PATH that omits homebrew and version-manager
	// shims, so tmux/opencode/git look unavailable. Recover the login
	// shell's PATH before any exec.LookPath runs.
	toolPathErr := toolpath.Ensure()

	addr := flag.String("addr", "127.0.0.1:8228", "listen address")
	guiMode := flag.Bool("gui", isAppBundle(), "open a native desktop window (Wails) instead of just serving HTTP")
	guiBootTimeout := flag.Duration("gui-boot-timeout", 10*time.Second, "how long --gui waits for the backend to answer before showing an error")
	guiAddr := flag.String("gui-addr", "127.0.0.1:0", "listen address for the backend when --gui is set (default picks an ephemeral port)")
	dbPath := flag.String("db", db.DefaultDBPath(), "path to opencode.db")
	historyPath := conversationHistoryFlag(flag.CommandLine)
	platformNames := make([]string, 0, len(knownPlatforms))
	for name := range knownPlatforms {
		platformNames = append(platformNames, name)
	}
	sort.Strings(platformNames)
	knownPlatformNames := strings.Join(platformNames, ", ")
	platformsFlag := flag.String("platforms", platformsettings.Default, "override saved platforms with a comma-separated list ("+knownPlatformNames+")")
	authPassword := flag.String("auth-password", "", "password required to access ocman (prefer "+authPasswordEnv+" env or -auth-password-file)")
	authPasswordFile := flag.String("auth-password-file", "", "read auth password from file (trimmed of trailing whitespace)")
	authSessionTTL := flag.Duration("auth-session-ttl", 30*24*time.Hour, "auth cookie lifetime")
	authTrustLocalhost := flag.Bool("auth-trust-localhost", false, "exempt loopback clients from auth (dev-mode escape hatch; also OCMAN_AUTH_TRUST_LOCALHOST)")
	opencodePasswordFile := flag.String("opencode-server-password-file", "", "read the managed OpenCode API password from a file")
	opencodeGeneratePassword := flag.Bool("opencode-server-generate-password", false, "generate an ephemeral managed OpenCode API password at startup")
	otelEndpoint := flag.String("otel", "", "OTLP/HTTP endpoint URL (e.g. http://localhost:4318). Empty disables telemetry. Falls back to OTEL_EXPORTER_OTLP_ENDPOINT.")
	autoApprove := flag.Bool("auto-approve", false, "default new sessions to auto-approve mode (uses OpenCode's running instance as the LLM judge)")
	mcpAddr := flag.String("mcp-addr", "127.0.0.1:8227", "loopback listen address for the MCP endpoint; local clients reach it without auth, so it must be a loopback address. Empty disables the dedicated listener (/mcp then only lives on -addr, behind auth).")
	publicBaseURL := flag.String("public-base-url", "", "externally reachable base URL for share links (e.g. https://ocman.example.com); falls back to "+publicBaseURLEnv+" env, then the request Host")
	relayURL := flag.String("relay-url", "", "base URL of the share relay for cross-machine sharing (e.g. https://share.example.com); falls back to "+share.RelayURLEnv+" env, then the built-in default")
	remoteListen := flag.String("remote-listen", "", "bind address for the remote-access gRPC server (e.g. 0.0.0.0:8230); empty disables it (multi-remote support)")
	remoteTLSCert := flag.String("remote-tls-cert", "", "TLS certificate file for the remote-access gRPC server (enables TLS together with -remote-tls-key)")
	remoteTLSKey := flag.String("remote-tls-key", "", "TLS key file for the remote-access gRPC server")
	remoteTrustedOverlay := flag.Bool("remote-trusted-overlay", false, "explicitly allow plaintext remote gRPC on a trusted overlay network")
	insecureNoAuth := flag.Bool("insecure-no-auth", false, "allow a non-loopback -addr/-gui-addr or a public -public-base-url with no password configured (also "+insecureNoAuthEnv+"=1)")
	logFile := flag.String("log-file", "", "log file path in addition to stderr (empty = ~/Library/Logs/ocman/ocman.log on macOS, else $XDG_STATE_HOME/ocman/ocman.log; \"-\" or \"off\" = stderr only)")
	flag.Parse()
	home, _ := os.UserHomeDir()
	logPath := setupLogFile(resolveLogPath(*logFile, runtime.GOOS, home, os.Getenv("XDG_STATE_HOME")))
	// Every startup failure goes through fatal: in GUI mode it shows a
	// native alert instead of the app silently vanishing.
	gui.SetFatalContext(*guiMode, logPath)
	gui.Version = version
	if toolPathErr != nil {
		log.WithError(toolPathErr).Warn("login shell PATH unavailable; tools may look missing")
	}
	fatal := gui.Fatalf
	if err := validateRemoteTransport(*remoteListen, *remoteTLSCert, *remoteTLSKey, *remoteTrustedOverlay); err != nil {
		fatal("%v", err)
	}

	// Refuse to expose an unauthenticated dashboard beyond loopback:
	// every session route (message, command, shell) would be usable by
	// anyone who can reach the port.
	authPasswordValue, err := resolveAuthPassword(*authPassword, *authPasswordFile)
	if err != nil {
		fatal("Failed to configure auth: %v", err)
	}
	httpListenAddr := *addr
	if *guiMode {
		httpListenAddr = *guiAddr
	}
	// Resolved before the exposure check: a public base URL means a reverse
	// proxy fronts the loopback listener, and every proxied request then
	// looks loopback to the auth middleware. Flag wins, then env.
	resolvedBaseURL := *publicBaseURL
	if resolvedBaseURL == "" {
		resolvedBaseURL = strings.TrimSpace(os.Getenv(publicBaseURLEnv))
	}
	if err := validateListenExposure(httpListenAddr, resolvedBaseURL, authPasswordValue != "",
		*insecureNoAuth || parseBoolEnv(os.Getenv(insecureNoAuthEnv))); err != nil {
		fatal("%v", err)
	}

	// Resolve the share relay: flag, then env, then the built-in
	// default. An invalid value stops startup rather than silently
	// disabling cross-machine sharing.
	resolvedRelayURL, relaySource, err := share.ResolveRelayURL(*relayURL, os.Getenv(share.RelayURLEnv), share.DefaultRelayURL)
	if err != nil {
		fatal("%v", err)
	}
	if resolvedRelayURL != "" {
		log.WithFields(log.Fields{"url": resolvedRelayURL, "source": relaySource}).
			Info("share relay configured")
	}

	// Initialise telemetry before opening either database.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	shutdownTel, err := telemetry.Init(ctx, *otelEndpoint, version)
	if err != nil {
		fatal("Failed to initialise telemetry: %v", err)
	}
	defer func() {
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdownTel(sctx); err != nil {
			log.WithError(err).Warn("OTel shutdown error")
		}
	}()

	startupPassword, err := resolveOpenCodePassword(*opencodePasswordFile, *opencodeGeneratePassword)
	if err != nil {
		fatal("Failed to configure OpenCode server auth: %v", err)
	}
	serve := func(ctx context.Context, listener net.Listener, reload func()) error {
		ctx, cancel := context.WithCancel(ctx)
		stateDBPath := state.DefaultDBPath()
		stateDB, err := state.Open(stateDBPath, server.ModuleSchemas()...)
		if err != nil {
			fatal("Failed to open state database: %v", err)
		}
		defer stateDB.Close()
		historyDB, err := openApplicationHistory(ctx, stateDB, stateDBPath, *historyPath, os.Getenv)
		if err != nil {
			cancel()
			return err
		}
		if historyDB != nil {
			defer historyDB.Close()
		}
		platformsExplicit := false
		flag.Visit(func(f *flag.Flag) { platformsExplicit = platformsExplicit || f.Name == "platforms" })
		if *platformsFlag, err = platformsettings.Resolve(ctx, stateDB, *platformsFlag, platformsExplicit); err != nil {
			fatal("Failed to load platform settings: %v", err)
		}
		// Parse and validate the selected platforms.
		enabledPlatforms := map[string]bool{}
		for _, name := range strings.Split(*platformsFlag, ",") {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			if knownPlatforms[name] == nil {
				fatal("Unknown platform: %q (known: %s)", name, knownPlatformNames)
			}
			enabledPlatforms[name] = true
		}
		if len(enabledPlatforms) == 0 {
			fatal("No platforms enabled. Use -platforms with at least one of: %s", knownPlatformNames)
		}
		// OpenCode's skill directory is touched only when OpenCode is enabled (FR-1).
		if enabledPlatforms[string(opencodeplatform.PlatformID)] {
			if err := opencodeskills.Remove("ocman-session-splitting"); err != nil {
				log.WithError(err).Warn("removing retired ocman session-splitting skill")
			}
			if err := opencodeskills.Remove("ocman-workflows"); err != nil {
				log.WithError(err).Warn("removing retired ocman workflows skill")
			}
			if err := opencodeskills.Install(embeddedSkills()); err != nil {
				log.WithError(err).Warn("installing embedded ocman skills")
			}
		}

		// Open the OpenCode database only when the opencode platform is enabled.
		var database *db.DB
		openCodeDBPath := "" // maintenance is offered only for an opened database
		var startupIssues []server.StartupIssue
		dbExplicit := false
		flag.Visit(func(f *flag.Flag) { dbExplicit = dbExplicit || f.Name == "db" })
		if enabledPlatforms[string(opencodeplatform.PlatformID)] {
			*dbPath = pinOpenCodeDB(*dbPath, dbExplicit)
			var issue *server.StartupIssue
			database, issue, err = openOpenCodeDB(*dbPath)
			if err != nil {
				fatal("%v", err)
			}
			if issue != nil {
				// Run as if opencode were omitted from -platforms.
				log.Warnf("%s; starting without the OpenCode platform. %s", issue.Message, issue.Hint)
				startupIssues = append(startupIssues, *issue)
				delete(enabledPlatforms, string(opencodeplatform.PlatformID))
			}
		}
		if database != nil {
			defer database.Close()
			openCodeDBPath = *dbPath
		}

		// Before anything inherits from it: Claude approvals recorded before
		// exact patterns would otherwise inherit as globs.
		if err := stateDB.EscapeLegacyApprovalPatterns(context.Background(), providerprotocol.ExactPattern); err != nil {
			fatal("Failed to upgrade Claude approval patterns: %v", err)
		}

		useService := database != nil && sharesServiceDB(openCodeDBPath)
		opencodePassword, err := startupOpenCodePassword(ctx, stateDB, startupPassword, enabledPlatforms[string(opencodeplatform.PlatformID)], useService)
		if err != nil {
			fatal("Failed to configure OpenCode v2 server auth: %v", err)
		}
		opencodeAuth := ocapi.New(opencodePassword)

		// Ensure this instance has a stable random identity + remote-access
		// token (multi-remote support). Generated and persisted on first
		// startup; reused thereafter. No networking is started here — the
		// gRPC remote-listen server is opt-in via -remote-listen.
		ident, err := stateDB.InstanceIdentity(ctx)
		if err != nil {
			fatal("Failed to ensure instance identity: %v", err)
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
		var integrations []platforms.Integration
		for id, enabled := range enabledPlatforms {
			if enabled {
				platform := knownPlatforms[id](ctx, database, stateDB, opencodeAuth)
				integrations = append(integrations, platform)
				if closer, ok := platform.(interface{ Close() }); ok {
					// Runs after cancel (deferred later): joins the platform's
					// goroutines before state.db closes.
					defer closer.Close()
				}
			}
		}
		preparation, err := composeCanonicalSessionCore(ctx, stateDB, historyDB, registry, integrations, ident.InstanceID)
		if err != nil {
			startupErr := canonicalStartupError(ctx, err)
			cancel()
			return startupErr
		}
		auth, err := buildAuth(ctx, stateDB, *authPassword, *authPasswordFile, *authSessionTTL, *addr, *authTrustLocalhost)
		if err != nil {
			fatal("Failed to configure auth: %v", err)
		}

		// In GUI mode the server listens on an ephemeral loopback port that
		// is only reachable from the Wails WebView proxy.  In headless mode
		// it listens on *addr as before.
		srv := server.New(database, stateDB, listener.Addr().String(), registry, auth).
			WithSessionPreparation(preparation).
			WithConversationHistory(historyDB, ident.InstanceID).
			WithOpenCodeAuth(opencodeAuth).
			WithAutoApproveDefault(*autoApprove).
			WithPublicBaseURL(resolvedBaseURL).
			WithRelay(resolvedRelayURL, relaySource).
			WithMCPAddr(*mcpAddr).
			WithOpenCodeDBPath(openCodeDBPath).
			WithOpenCodeService(useService).
			WithLogPath(logPath).
			WithVersion(version).
			WithStartupIssues(startupIssues...).
			WithToolPathError(toolPathErr).
			WithConfigReload(reload, *platformsFlag, platformsExplicit)
		if *guiMode {
			srv.WithRemoteAccess(ident.InstanceID, "", false, false)
		} else {
			// Start the remote-access gRPC server when -remote-listen is set
			// (multi-remote support). Off by default so single-host installs
			// are byte-for-byte unchanged (NFR-6).
			listening, listenAddr, tlsOn, stopRemote := startRemoteServer(ctx, srv, stateDB, ident, *remoteListen, *remoteTLSCert, *remoteTLSKey, *remoteTrustedOverlay)
			defer stopRemote()
			srv.WithRemoteAccess(ident.InstanceID, listenAddr, listening, tlsOn)

			// Start the hub-side remote manager: it loads any saved remotes
			// from state.db and dials them in the background, registering
			// remote platform/host adapters as they connect. With zero saved
			// remotes this is a no-op (NFR-6).
			mgr := remote.NewManager(srv.Registry(), srv.HostRouter(), stateDB, string(opencodeplatform.PlatformID))
			srv.SetRemoteManager(mgr)
			// Stop after Start returns: the remotes module's inventory loop
			// has been joined by then, and state.db closes after this.
			defer startPreparedRemoteManager(ctx, srv, mgr)()
		}
		defer cancel()
		defer opencodeplatform.ResetCaches()
		return srv.StartOnListener(ctx, listener)
	}
	start := func(ctx context.Context, listener net.Listener) error { return startBackend(ctx, listener, serve) }
	if *guiMode {
		err = gui.RunGUIWithStarter(ctx, start, httpListenAddr, state.DefaultDBPath(), *guiBootTimeout)
	} else {
		var listener net.Listener
		listener, err = net.Listen("tcp", *addr)
		if err == nil {
			err = start(ctx, listener)
		}
	}
	if err != nil {
		fatal("Server error: %v", err)
	}

	log.Info("server stopped gracefully")
}
