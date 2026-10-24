package claudecode

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/NoUseFreak/ocman/internal/platforms/claude/provider"
	"github.com/NoUseFreak/ocman/internal/providersupervisor"
	"github.com/NoUseFreak/ocman/internal/state"
)

// providerArgs are the flags of `ocman provider <id>`.
type providerArgs struct {
	Repo, Spool string
	DataDir     string // ocman's data directory; empty is the default
	MCPAddr     string // bound loopback MCP listener; empty omits MCP
	PluginDir   string // ocman's skills plugin; empty omits it
	// The dashboard's claude executable and CLAUDE_CONFIG_DIR, carried
	// explicitly: tmux starts the supervisor with its server's environment,
	// which may predate the dashboard's.
	CLI       string
	ConfigEnv *string // nil when not passed (inherit)
}

func claudeSupervisor(ctx context.Context, a providerArgs) (*providersupervisor.Supervisor, error) {
	return claudeprovider.NewSupervisor(ctx, claudeProjectOptions(a), a.Spool)
}

// claudeProjectOptions configures a project supervisor: the default pool
// policy (cap 4 across the machine, 600 s idle), the index synced by ocman's
// browsing supervisor, and ocman's MCP server and skills for every child.
func claudeProjectOptions(a providerArgs) claudeprovider.Options {
	dataDir := a.DataDir
	if dataDir == "" {
		dataDir = state.DefaultDataDir()
	}
	opts := claudeprovider.DefaultOptions(dataDir)
	opts.NoSync = true
	opts.ProjectRoot = a.Repo
	opts.PluginDir = a.PluginDir
	if a.CLI != "" {
		opts.CLI = a.CLI
	}
	if a.ConfigEnv != nil {
		opts.ConfigEnv = a.ConfigEnv
		opts.ConfigDir = claudeprovider.ConfigDir(*a.ConfigEnv)
	}
	if a.MCPAddr != "" {
		opts.MCPConfig = claudeprovider.MCPConfig(a.MCPAddr)
	}
	return opts
}

// Built-in provider implementations register their factory here, keyed by
// the id the runtime launches (the platform id) and the short alias.
var providerFactories = map[string]func(context.Context, providerArgs) (*providersupervisor.Supervisor, error){
	"claude":                  claudeSupervisor,
	claudeprovider.PlatformID: claudeSupervisor,
}

func providerCommand(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: ocman provider <id> --socket <path> --spool <path> --repo <path>")
	}
	flags := flag.NewFlagSet("provider", flag.ContinueOnError)
	socket := flags.String("socket", "", "private unix socket path")
	var a providerArgs
	flags.StringVar(&a.Spool, "spool", "", "per-session spool directory")
	flags.StringVar(&a.Repo, "repo", "", "main repository checkout")
	flags.StringVar(&a.DataDir, "data-dir", "", "ocman data directory")
	flags.StringVar(&a.MCPAddr, "mcp-addr", "", "ocman's bound MCP listener address")
	flags.StringVar(&a.PluginDir, "plugin-dir", "", "ocman's skills plugin directory")
	flags.StringVar(&a.CLI, "claude-cli", "", "the dashboard's claude executable")
	configEnv := flags.String("claude-config-dir", "", "the dashboard's CLAUDE_CONFIG_DIR; empty unsets it")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "claude-config-dir" {
			a.ConfigEnv = configEnv
		}
	})
	if flags.NArg() != 0 || !filepath.IsAbs(*socket) || !filepath.IsAbs(a.Spool) || !filepath.IsAbs(a.Repo) {
		return fmt.Errorf("provider requires absolute socket, spool and repo paths")
	}
	factory := providerFactories[args[0]]
	if factory == nil {
		return fmt.Errorf("provider %q is not registered", args[0])
	}
	supervisor, err := factory(ctx, a)
	if err != nil {
		return err
	}
	return supervisor.Serve(ctx, *socket)
}

// RunProviderCommand handles the provider subcommand, reporting failures on stderr.
func RunProviderCommand(args []string) bool {
	if len(args) == 0 || args[0] != "provider" {
		return false
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := providerCommand(ctx, args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	return true
}
