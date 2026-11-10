// Package app composes ocman's platforms, databases, servers and desktop shell.
package app

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/gui"
	"github.com/ocman/ocman/internal/mcpauth"
	"github.com/ocman/ocman/internal/platformconnections"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/claude/transport"
	codextransport "github.com/ocman/ocman/internal/platforms/codex/transport"
	"github.com/ocman/ocman/internal/platforms/opencode/api"
	opencodeplatform "github.com/ocman/ocman/internal/platforms/opencode/provider"
	"github.com/ocman/ocman/internal/platformsettings"
	"github.com/ocman/ocman/internal/pricing"
	"github.com/ocman/ocman/internal/providerprotocol"
	"github.com/ocman/ocman/internal/remote"
	"github.com/ocman/ocman/internal/server"
	"github.com/ocman/ocman/internal/share"
	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/telemetry"
	"github.com/ocman/ocman/internal/toolpath"
)

var version = "dev"
var skillCatalog map[string][]byte

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
	configPath := flag.String("config", defaultConfigPath(), "configuration file (also OCMAN_CONFIG; migrated from saved settings on first startup)")
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
	mcpAddr := flag.String("mcp-addr", "127.0.0.1:8227", "loopback listen address for the MCP endpoint; local clients authenticate with the bearer token in <state dir>/mcp-token, and it must be a loopback address. Empty disables the dedicated listener (/mcp then only lives on -addr, behind auth).")
	publicBaseURL := flag.String("public-base-url", "", "externally reachable base URL for share links (e.g. https://ocman.example.com); falls back to "+publicBaseURLEnv+" env, then the request Host")
	relayURL := flag.String("relay-url", "", "base URL of the share relay for cross-machine sharing (e.g. https://share.example.com); falls back to "+share.RelayURLEnv+" env, then the built-in default")
	remoteListen := flag.String("remote-listen", "", "bind address for the remote-access gRPC server (e.g. 0.0.0.0:8230); empty disables it (multi-remote support)")
	remoteTLSCert := flag.String("remote-tls-cert", "", "TLS certificate file for the remote-access gRPC server (enables TLS together with -remote-tls-key)")
	remoteTLSKey := flag.String("remote-tls-key", "", "TLS key file for the remote-access gRPC server")
	remoteTrustedOverlay := flag.Bool("remote-trusted-overlay", false, "explicitly allow plaintext remote gRPC on a trusted overlay network")
	remoteManaged := flag.Bool("remote-managed", false, "run as a managed remote: auto-approve settings come from the connected hub instead of this machine (requires -remote-listen)")
	insecureNoAuth := flag.Bool("insecure-no-auth", false, "allow a non-loopback -addr/-gui-addr or a public -public-base-url with no password configured (also "+insecureNoAuthEnv+"=1)")
	logFile := flag.String("log-file", "", "log file path in addition to stderr (empty = ~/Library/Logs/ocman/ocman.log on macOS, else $XDG_STATE_HOME/ocman/ocman.log; \"-\" or \"off\" = stderr only)")
	resolvedConfigPath, configErr := prepareFileConfig(flag.CommandLine, os.Args[1:], *configPath)
	if configErr != nil {
		setupFailedConfigDiagnostics(flag.CommandLine, os.Args[1:])
		gui.Fatalf("Failed to load configuration: %v", configErr)
	}
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
	if err := resolveConfigAuth(flag.CommandLine, os.Args[1:]); err != nil {
		fatal("Failed to load authentication configuration: %v", err)
	}
	if err := validateRemoteTransport(*remoteListen, *remoteTLSCert, *remoteTLSKey, *remoteTrustedOverlay); err != nil {
		fatal("%v", err)
	}
	if err := validateRemoteManaged(*remoteListen, *remoteManaged, *guiMode); err != nil {
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
		*authTrustLocalhost || parseBoolEnv(os.Getenv(authTrustLocalhostEnv)),
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
		existed, err := existingInstallation(stateDBPath)
		if err != nil {
			cancel()
			return err
		}
		stateDB, err := state.Open(stateDBPath, server.ModuleSchemas()...)
		if err != nil {
			fatal("Failed to open state database: %v", err)
		}
		defer stateDB.Close()
		if err := stateDB.EnableFileConfig(ctx, resolvedConfigPath); err != nil {
			cancel()
			return fmt.Errorf("loading file configuration: %w", err)
		}
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
		enabledPlatforms, err := selectedPlatforms(*platformsFlag, knownPlatformNames)
		if err != nil {
			fatal("%v", err)
		}
		var startupIssues []server.StartupIssue
		if platformsExplicit {
			if issue := requireOpenCodeV2(enabledPlatforms); issue != nil {
				startupIssues = append(startupIssues, *issue)
			}
		}

		configs := connectionConfigurations()
		var connections *platformconnections.Store
		if !platformsExplicit {
			connections, err = initializeConnections(ctx, stateDB, existed, enabledPlatforms, configs)
			if err != nil {
				cancel()
				return err
			}
		}
		var database *db.DB
		openCodeDBPath := *dbPath
		dbExplicit := false
		flag.Visit(func(f *flag.Flag) { dbExplicit = dbExplicit || f.Name == "db" })
		if !dbExplicit && os.Getenv("OPENCODE_DB") != "" {
			openCodeDBPath = os.Getenv("OPENCODE_DB")
		}
		if openCodeDBPath == "" {
			openCodeDBPath = db.DefaultDBPath()
		}
		if platformsExplicit && enabledPlatforms["opencode"] {
			openCodeDBPath = pinOpenCodeDB(openCodeDBPath, dbExplicit)
			var issue *server.StartupIssue
			database, issue, err = openOpenCodeDB(openCodeDBPath)
			if err != nil {
				cancel()
				return err
			}
			if issue != nil {
				startupIssues = append(startupIssues, *issue)
				delete(enabledPlatforms, "opencode")
			}
			if database != nil {
				defer database.Close()
			}
			installLegacyOpenCodeSkills()
		}

		// Before anything inherits from it: Claude approvals recorded before
		// exact patterns would otherwise inherit as globs.
		if err := stateDB.EscapeLegacyApprovalPatterns(context.Background(), providerprotocol.ExactPattern); err != nil {
			fatal("Failed to upgrade Claude approval patterns: %v", err)
		}

		useService := platformsExplicit && database != nil && sharesServiceDB(openCodeDBPath)
		opencodePassword, err := startupOpenCodePassword(ctx, stateDB, startupPassword, platformsExplicit && enabledPlatforms["opencode"], useService)
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
		if err := enableRemoteManaged(ctx, stateDB, *remoteManaged); err != nil {
			fatal("Failed to load managed remote settings: %v", err)
		}
		if connections != nil {
			if err := bindMigratedHistorySources(ctx, connections, historyDB, ident.InstanceID); err != nil {
				cancel()
				return err
			}
		}

		// Pre-warm the pricing table in the background so the first metrics request
		// doesn't block on a remote fetch.
		go pricing.Load()

		// Saved named connections are authoritative unless -platforms overrides.
		registry := platforms.NewRegistry()
		var integrations []platforms.Integration
		var keys []platforms.ID
		factory := connectionFactory{state: stateDB, dataDir: filepath.Dir(stateDBPath), mcpAddr: *mcpAddr, openCodeDB: openCodeDBPath, legacyAuth: opencodeAuth}
		if connections != nil {
			blocked, issues, checkErr := blockedConnectionKinds(ctx, connections)
			if checkErr != nil {
				cancel()
				return checkErr
			}
			factory.blockedKinds = blocked
			startupIssues = append(startupIssues, issues...)
		}
		if platformsExplicit {
			for id, enabled := range enabledPlatforms {
				if enabled {
					if p := knownPlatforms[id](ctx, database, stateDB, opencodeAuth); p != nil {
						integrations = append(integrations, p)
					}
				}
			}
		} else {
			integrations, keys, err = factory.buildCollection(ctx, connections)
			if err != nil {
				cancel()
				return err
			}
		}
		for _, p := range integrations {
			if closer, ok := p.(interface{ Close() }); ok {
				defer closer.Close()
			}
		}
		var connectionRouter *platformconnections.Router
		if connections != nil {
			connectionRouter = platformconnections.NewRouter(connections)
		}
		preparation, err := composeConnectionSessionCore(ctx, stateDB, historyDB, registry, integrations, ident.InstanceID, keys, connections, connectionRouter)
		if err != nil {
			startupErr := canonicalStartupError(ctx, err)
			cancel()
			return startupErr
		}
		if connections != nil {
			if err := registerRetainedConnections(ctx, stateDB, historyDB, registry, connectionRouter, connections, ident.InstanceID); err != nil {
				cancel()
				return err
			}
		}
		lifecycle := connectionLifecycle(factory, connectionRouter, keys, integrations)
		auth, err := buildAuth(ctx, stateDB, *authPassword, *authPasswordFile, *authSessionTTL, *addr, *authTrustLocalhost)
		if err != nil {
			fatal("Failed to configure auth: %v", err)
		}

		// In GUI mode the server listens on an ephemeral loopback port that
		// is only reachable from the Wails WebView proxy.  In headless mode
		// it listens on *addr as before.
		srv := server.New(database, stateDB, listener.Addr().String(), registry, auth).
			WithPlatformConnections(connectionRouter, connections, ident.InstanceID, lifecycle, configs).
			WithSessionPreparation(preparation).
			WithConversationHistory(historyDB, ident.InstanceID).
			WithOpenCodeAuth(opencodeAuth).
			WithAutoApproveDefault(*autoApprove).
			WithPublicBaseURL(resolvedBaseURL).
			WithRelay(resolvedRelayURL, relaySource).
			WithMCPAddr(*mcpAddr).
			WithMCPTokenFile(mcpauth.TokenPath(state.DefaultDataDir())).
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
			useHubManagedSettings(mgr, srv, stateDB, ident.InstanceID)
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
