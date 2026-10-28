package claudecode

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/providersupervisor"
	"github.com/ocman/ocman/internal/providersupervisor/providertest"
)

func TestProviderCommandValidation(t *testing.T) {
	for _, args := range [][]string{nil, {"unknown"}, {"unknown", "--bad"}, {"unknown", "--socket", "/s", "--spool", "/sp", "--repo", "/r"}, {"unknown", "--socket", "relative", "--spool", "/sp", "--repo", "/r"}, {"unknown", "--socket", "/s", "--spool", "/sp", "--repo", "/r", "extra"}} {
		if err := providerCommand(t.Context(), args); err == nil {
			t.Fatal(args)
		}
	}
	if RunProviderCommand(nil) || RunProviderCommand([]string{"--help"}) {
		t.Fatal("dashboard args intercepted")
	}
}

func TestProviderCommandUsesRegisteredSupervisor(t *testing.T) {
	dir := providertest.Directory(t)
	old := providerFactories
	t.Cleanup(func() { providerFactories = old })
	providerFactories = map[string]func(context.Context, providerArgs) (*providersupervisor.Supervisor, error){
		"fake": func(_ context.Context, a providerArgs) (*providersupervisor.Supervisor, error) {
			if a.Repo != dir || a.MCPAddr != "127.0.0.1:9" || a.PluginDir != "/plugin" || a.DataDir != "/data" {
				t.Fatal(a)
			}
			return providersupervisor.New(a.Spool, providersupervisor.Backend{})
		},
		"broken": func(context.Context, providerArgs) (*providersupervisor.Supervisor, error) {
			return nil, errors.New("factory failed")
		},
	}
	args := []string{"fake", "--socket", filepath.Join(dir, "s"), "--spool", filepath.Join(dir, "sp"), "--repo", dir,
		"--mcp-addr", "127.0.0.1:9", "--plugin-dir", "/plugin", "--data-dir", "/data"}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := providerCommand(ctx, args); err != nil {
		t.Fatal(err)
	}
	args[0] = "broken"
	if err := providerCommand(ctx, args); err == nil || err.Error() != "factory failed" {
		t.Fatal(err)
	}
}

// A project supervisor keeps the machine-wide pool policy and gets ocman's
// bound MCP listener and skills.
func TestClaudeProjectOptions(t *testing.T) {
	opts := claudeProjectOptions(providerArgs{DataDir: "/data", MCPAddr: "127.0.0.1:41234", PluginDir: "/data/plugin"})
	if opts.MaxProcs != 4 || opts.IdleTimeout != 600*time.Second || opts.SlotDir != "/data/claude-slots" || !opts.NoSync ||
		opts.StatePath != "/data/claude-sessions.json" || opts.IndexPath != "/data/claude-index.db" || opts.PluginDir != "/data/plugin" ||
		!strings.Contains(opts.MCPConfig, "http://127.0.0.1:41234/mcp") {
		t.Fatalf("%+v", opts)
	}
	if opts := claudeProjectOptions(providerArgs{}); opts.MCPConfig != "" || opts.PluginDir != "" || opts.SlotDir == "" {
		t.Fatalf("%+v", opts)
	}
	if providerFactories["claude"] == nil || providerFactories["claudecode"] == nil {
		t.Fatal("claude provider not registered under its platform id")
	}
}

// The dashboard's claude and CLAUDE_CONFIG_DIR reach the supervisor as
// flags; an empty config value unsets it for the children.
func TestClaudeProjectOptionsCarryDashboardConfiguration(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "/stale")
	config := "/active"
	opts := claudeProjectOptions(providerArgs{DataDir: "/data", CLI: "/bin/claude", ConfigEnv: &config})
	if opts.CLI != "/bin/claude" || opts.ConfigDir != "/active" || opts.ConfigEnv == nil || *opts.ConfigEnv != "/active" || opts.LaunchPath != "/data/claude-launch.json" {
		t.Fatalf("%+v", opts)
	}
	empty := ""
	if opts := claudeProjectOptions(providerArgs{ConfigEnv: &empty}); opts.ConfigDir == "/stale" || *opts.ConfigEnv != "" {
		t.Fatalf("unset config = %+v", opts)
	}
	if opts := claudeProjectOptions(providerArgs{}); opts.ConfigEnv != nil || opts.ConfigDir != "/stale" {
		t.Fatalf("without flags the environment is inherited: %+v", opts)
	}
}
