// Package codextransport composes Codex into ocman: one machine-wide
// supervisor under tmux drives sessions, and a read-only app-server serves
// history to ingestion.
package codextransport

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	codexprovider "github.com/ocman/ocman/internal/platforms/codex/provider"
)

// supervisorArgs are the flags of `ocman provider codex`.
type supervisorArgs struct {
	Socket, Spool, Repo string
	CLI                 string // the dashboard's codex executable
	Home                string // canonical Codex store and account directory
	MCPAddr             string // ocman's bound MCP listener; empty omits it
	Version             string
}

func parseSupervisorArgs(args []string) (supervisorArgs, error) {
	flags := flag.NewFlagSet("provider codex", flag.ContinueOnError)
	var a supervisorArgs
	flags.StringVar(&a.Socket, "socket", "", "private unix socket path")
	flags.StringVar(&a.Spool, "spool", "", "per-session spool directory")
	flags.StringVar(&a.Repo, "repo", "", "supervisor root directory")
	flags.StringVar(&a.CLI, "codex-cli", "codex", "the dashboard's codex executable")
	flags.StringVar(&a.Home, "codex-home", "", "the dashboard's canonical Codex home")
	flags.StringVar(&a.MCPAddr, "mcp-addr", "", "ocman's bound MCP listener address")
	flags.StringVar(&a.Version, "ocman-version", "dev", "ocman version reported to Codex")
	if err := flags.Parse(args); err != nil {
		return a, err
	}
	if flags.NArg() != 0 || !filepath.IsAbs(a.Socket) || !filepath.IsAbs(a.Spool) || !filepath.IsAbs(a.Repo) {
		return a, fmt.Errorf("provider codex requires absolute socket, spool and repo paths")
	}
	return a, nil
}

// options configures the supervisor's app-server: ocman's MCP server is
// offered to every thread through a config override.
func (a supervisorArgs) options() codexprovider.Options {
	opts := codexprovider.Options{CLI: a.CLI, Dir: a.Repo, Version: a.Version}
	if a.Home != "" {
		opts.Env = append(os.Environ(), "CODEX_HOME="+a.Home)
	}
	if a.MCPAddr != "" {
		opts.Args = []string{"-c", `mcp_servers.ocman.url="http://` + a.MCPAddr + `/mcp"`}
	}
	return opts
}

func runSupervisor(ctx context.Context, args []string) error {
	a, err := parseSupervisorArgs(args)
	if err != nil {
		return err
	}
	sup, err := codexprovider.NewSupervisor(ctx, a.options(), a.Spool)
	if err != nil {
		return err
	}
	return sup.Serve(ctx, a.Socket)
}

// RunProviderCommand handles `ocman provider codex ...`, reporting failures
// on stderr. Other provider ids are left to their own platforms.
func RunProviderCommand(args []string) bool {
	if len(args) < 2 || args[0] != "provider" || args[1] != codexprovider.PlatformID {
		return false
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := runSupervisor(ctx, args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	return true
}
