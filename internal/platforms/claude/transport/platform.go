// Package claudecode routes Claude sessions through browsing and project supervisors.
package claudecode

import (
	"context"
	"github.com/ocman/ocman/internal/autoapprove"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/historyjournal"
	"github.com/ocman/ocman/internal/hostsvc/local"
	"github.com/ocman/ocman/internal/ocruntime"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/claude/launch"
	"github.com/ocman/ocman/internal/platforms/claude/provider"
	"github.com/ocman/ocman/internal/platforms/provider"
	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/tmux"
)

// New serves the machine's Claude transcripts to history ingestion and drives the sessions ocman launches through per-project supervisors.
func New(ctx context.Context, database *db.DB, stateDB *state.DB, skills map[string][]byte, mcpAddr string) platforms.Integration {
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
	// Status hooks need ocman's listener; history hooks only need the journal,
	// so terminal sessions are recorded even while ocman is stopped.
	journal, err := historyjournal.Dir(string(claudeprovider.PlatformID))
	if err != nil {
		log.WithError(err).Warn("Claude history journal unavailable; terminal sessions sync only at startup")
		journal = ""
	}
	statusAddr := mcpAddr
	hook := ""
	if mcpAddr != "" || journal != "" {
		hook = claudeprovider.HookCommand(exe, mcpAddr, journal)
	}
	marketplace := filepath.Join(dataDir, "claude-marketplace")
	pluginDir, err := claudeprovider.WritePlugin(marketplace, skills, hook)
	if err != nil {
		log.WithError(err).Warn("writing ocman's Claude plugin; launching without ocman skills")
		pluginDir, statusAddr = "", ""
	}
	// The directory catalog is served here: it lists ocman's plugin skills
	// like the children project supervisors launch with it.
	opts.PluginDir = pluginDir
	var history *claudeprovider.HistoryReader
	opts.HistoryReady = func(reader *claudeprovider.HistoryReader) { history = reader }
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
	adapter.SetRules(rules)
	p := &claudePlatform{Adapter: adapter, cli: opts.CLI, configDir: opts.ConfigDir, marketplace: marketplace, hooks: statusAddr != "", journal: journal, launchPath: opts.LaunchPath, slots: opts.SlotDir}
	p.history = history
	runtime := ocruntime.NewBackendRuntime(claudeprovider.PlatformID, exe, filepath.Join(dataDir, "p"))
	runtime.Args = func() []string {
		args := []string{"--data-dir", dataDir, "--plugin-dir", pluginDir, "--claude-cli", opts.CLI, "--claude-config-dir", configEnv}
		if addr, _ := p.mcpAddr.Load().(string); addr != "" {
			args = append(args, "--mcp-addr", addr)
		}
		return args
	}
	// Include SDK compatibility so pre-SDK supervisors drain before replacement.
	// The MCP address is not part of this identity (WriteLaunchConfig).
	runtime.Identity = func() string {
		return opts.CLI + "\x00" + configEnv + "\x00" + pluginDir + "\x00child-sessions/v2\x00native-permissions/v1\x00sdk-py/" + claudecli.SDKVersion
	}
	projectsCtx, stop := context.WithCancel(ctx)
	p.projects = &claudeProjects{ctx: projectsCtx, stop: stop, mgr: &local.ProviderManager{Runtime: runtime, State: stateDB}, rules: rules,
		statePath: opts.StatePath, roots: map[string]*claudeProject{}, draining: map[string]*drained{}, attached: make(chan struct{})}
	p.projects.mgr.OnDrain = p.projects.onDrain
	if stateDB != nil {
		p.projects.recover(projectsCtx)
	}
	if pluginDir != "" {
		p.ensurePlugin(projectsCtx)
	}
	return p
}

// ensurePlugin installs ocman's plugin, or refreshes Claude's cached copy,
// in the background: the CLI is slow and startup must not wait for it.
func (p *claudePlatform) ensurePlugin(ctx context.Context) {
	if _, err := exec.LookPath(p.cli); err != nil {
		return
	}
	p.pluginSync.Go(func() {
		action, err := claudeprovider.EnsurePlugin(ctx, p.cli, p.configDir, p.marketplace)
		switch {
		case err != nil && ctx.Err() == nil:
			log.WithError(err).Warn("installing ocman's Claude plugin; terminal sessions sync only at startup")
		case action != "":
			log.WithField("action", action).Info("ocman's Claude plugin is current; restart running Claude sessions to load its hooks")
		}
	})
}

// HistoryJournalDir is where the plugin's hooks record transcript changes.
func (p *claudePlatform) HistoryJournalDir() string { return p.journal }

// Close stops recovery and the project supervisors' event streams and waits
// for them; the supervisors themselves keep running (AD-4).
func (p *claudePlatform) Close() {
	p.projects.close()
	p.pluginSync.Wait()
	p.Adapter.Close()
}

// claudePlatform is the browsing adapter plus the project supervisors that
// drive launched sessions (projects.go).
type claudePlatform struct {
	history *claudeprovider.HistoryReader
	*provider.Adapter
	projects    *claudeProjects
	mcpAddr     atomic.Value // string: ocman's bound MCP listener
	launchPath  string       // where running supervisors read it
	cli         string
	configDir   string
	marketplace string // local marketplace holding ocman's plugin (AD-11)
	hooks       bool   // the plugin carries status hooks: the MCP listener is enabled
	journal     string // history journal the plugin's hooks append to
	slots       string // machine-wide claude process slots (claudeprovider.HelperSlot)
	pluginSync  sync.WaitGroup
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
