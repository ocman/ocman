// Package claudecode routes Claude sessions through browsing and project supervisors.
package claudecode

import (
	"context"
	"github.com/NoUseFreak/ocman/internal/autoapprove"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/claudeprovider"
	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/hostsvc/local"
	"github.com/NoUseFreak/ocman/internal/ocruntime"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/provider"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/tmux"
)

// New serves the machine's Claude transcripts for browsing
// and drives the sessions ocman launches through per-project supervisors.
func New(ctx context.Context, database *db.DB, stateDB *state.DB, skills map[string][]byte, mcpAddr string) platforms.Platform {
	exe, err := os.Executable()
	if err != nil {
		log.WithError(err).Warn("Claude Code sessions unavailable")
		return nil
	}
	p := newClaudePlatformAt(ctx, database, stateDB, state.DefaultDataDir(), exe, skills, mcpAddr)
	if p == nil {
		return nil // a typed nil would register a broken platform
	}
	return p
}

// newClaudePlatformAt is New with its data directory and the
// ocman executable launched as `ocman provider claudecode`. Browsing runs
// in-process; ocman still reaches it only through the provider protocol on a
// private socket, like a project supervisor. A failure leaves the platform
// unregistered rather than failing startup.
func newClaudePlatformAt(ctx context.Context, database *db.DB, stateDB *state.DB, dataDir, exe string, skills map[string][]byte, mcpAddr string) *claudePlatform {
	opts := claudeprovider.DefaultOptions(dataDir)
	// Project supervisors run under tmux, whose server environment may
	// predate this process: they get the resolved CLI and the active
	// CLAUDE_CONFIG_DIR explicitly (FR-3/4), never by re-resolving them.
	if cli, err := exec.LookPath(opts.CLI); err == nil {
		opts.CLI = cli
	}
	configEnv := os.Getenv("CLAUDE_CONFIG_DIR")
	hook := ""
	if mcpAddr != "" {
		hook = claudeprovider.HookCommand(exe, mcpAddr)
	}
	marketplace := filepath.Join(dataDir, "claude-marketplace")
	pluginDir, err := claudeprovider.WritePlugin(marketplace, skills, hook)
	if err != nil {
		log.WithError(err).Warn("writing ocman's Claude plugin; launching without ocman skills")
		hook, pluginDir = "", ""
	}
	// The directory catalog is served here: it lists ocman's plugin skills
	// like the children project supervisors launch with it.
	opts.PluginDir = pluginDir
	adapter, err := startClaudeBrowser(ctx, opts)
	if err != nil {
		log.WithError(err).Warn("Claude Code sessions unavailable")
		return nil
	}
	// Rulesets live in ocman, never in a supervisor (AD-7a). The server
	// starts the headless event streams once its prompt pipeline is installed;
	// lifecycle edges reach the queue, routines and plugin replies through
	// the registry's sink.
	rules, err := provider.OpenRules(filepath.Join(dataDir, "claude-rules.json"))
	if err != nil {
		// Never start without the saved denies.
		log.WithError(err).Error("Claude Code sessions unavailable: permission rules unreadable")
		return nil
	}
	if stateDB != nil {
		// Children forked before exact patterns hold their parent's
		// approvals as globs; never serve them before converting.
		approvals, err := stateDB.ListPlatformApprovals(ctx, string(claudeprovider.PlatformID))
		if err != nil {
			log.WithError(err).Error("Claude Code sessions unavailable: reading approvals to upgrade inherited rules")
			return nil
		}
		rules.EscapeInheritedApprovals(approvals)
	}
	adapter.SetRules(rules)
	if database != nil {
		// Claude rows join analytics-cache.db (AD-12): OpenCode's mirror,
		// or the analytics store of an ocman without OpenCode.
		database.AddAnalyticsSource(claudeprovider.AnalyticsSource(opts.IndexPath))
	}
	p := &claudePlatform{Adapter: adapter, cli: opts.CLI, configDir: opts.ConfigDir, marketplace: marketplace, hooks: hook != "", launchPath: opts.LaunchPath, slots: opts.SlotDir}
	runtime := ocruntime.NewProviderRuntime(claudeprovider.PlatformID, exe, filepath.Join(dataDir, "p"))
	runtime.Args = func() []string {
		args := []string{"--data-dir", dataDir, "--plugin-dir", pluginDir, "--claude-cli", opts.CLI, "--claude-config-dir", configEnv}
		if addr, _ := p.mcpAddr.Load().(string); addr != "" {
			args = append(args, "--mcp-addr", addr)
		}
		return args
	}
	// A supervisor launched with another CLI or account config is drained
	// and replaced; the MCP address is not part of it (WriteLaunchConfig).
	runtime.Identity = func() string { return opts.CLI + "\x00" + configEnv + "\x00" + pluginDir }
	p.projects = &claudeProjects{ctx: ctx, mgr: &local.ProviderManager{Runtime: runtime, State: stateDB}, rules: rules,
		statePath: opts.StatePath, roots: map[string]*claudeProject{}, draining: map[string]*drained{}, attached: make(chan struct{})}
	p.projects.mgr.OnDrain = p.projects.onDrain
	if stateDB != nil {
		p.projects.recover(ctx)
	}
	return p
}

// claudePlatform is the browsing adapter plus the project supervisors that
// drive launched sessions (projects.go).
type claudePlatform struct {
	*provider.Adapter
	projects    *claudeProjects
	mcpAddr     atomic.Value // string: ocman's bound MCP listener
	launchPath  string       // where running supervisors read it
	cli         string
	configDir   string
	marketplace string // local marketplace holding ocman's plugin (AD-11)
	hooks       bool   // the plugin carries hooks: the MCP listener is enabled
	slots       string // machine-wide claude process slots (claudeprovider.HelperSlot)
}

func (p *claudePlatform) RuntimeAvailable() bool {
	_, err := exec.LookPath(p.cli)
	return err == nil && tmux.IsAvailable()
}

// HookPlugin reports whether the hook plugin can be installed and is.
func (p *claudePlatform) HookPlugin() (available, installed bool) {
	return p.hooks, claudeprovider.PluginInstalled(p.configDir)
}

func (p *claudePlatform) InstallHookPlugin(ctx context.Context) error {
	return claudeprovider.InstallPlugin(ctx, p.cli, p.marketplace)
}

func (p *claudePlatform) UninstallHookPlugin(ctx context.Context) error {
	return claudeprovider.UninstallPlugin(ctx, p.cli)
}

// WorktreeName names automatic worktrees with a short `claude -p` Haiku call.
func (p *claudePlatform) WorktreeName(ctx context.Context, dir, prompt string) (string, error) {
	return claudeprovider.WorktreeName(ctx, p.slots, p.cli, dir, prompt)
}

// PermissionJudge judges permissions when no OpenCode instance is reachable
// (AD-8), bounded by the machine-wide helper budget.
func (p *claudePlatform) PermissionJudge() autoapprove.Judge {
	return autoapprove.ClaudeJudge{CLI: p.cli, Slot: func(ctx context.Context) (func(), error) {
		return claudeprovider.HelperSlot(ctx, p.slots)
	}}
}

func startClaudeBrowser(ctx context.Context, opts claudeprovider.Options) (*provider.Adapter, error) {
	dir, err := os.MkdirTemp("", "ocman-claude-") // short: unix socket path limit
	if err != nil {
		return nil, err
	}
	sup, err := claudeprovider.NewSupervisor(ctx, opts, filepath.Join(dir, "spool"))
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	socket := filepath.Join(dir, "s")
	go func() {
		if err := sup.Serve(ctx, socket); err != nil {
			log.WithError(err).Warn("Claude provider socket stopped")
		}
		_ = os.RemoveAll(dir)
	}()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		adapter, err := provider.New(ctx, socket)
		if err == nil || time.Now().After(deadline) {
			return adapter, err
		}
	}
}

// SetMCPAddr records ocman's bound MCP listener; the server calls it once
// the listener is up. New supervisors get it as a flag, and supervisors that
// outlived an earlier ocman read it for their next child, so a rebound
// listener (another port after a restart) works without a relaunch.
func (p *claudePlatform) SetMCPAddr(addr string) {
	p.mcpAddr.Store(addr)
	if p.launchPath != "" {
		if err := claudeprovider.WriteLaunchConfig(p.launchPath, addr); err != nil {
			log.WithError(err).Error("publishing the MCP address to Claude supervisors")
		}
	}
}

func (*claudePlatform) DisplayName() string {
	return platforms.DisplayName(claudeprovider.PlatformID)
}
