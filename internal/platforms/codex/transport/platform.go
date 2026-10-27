package codextransport

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/hostsvc/local"
	"github.com/NoUseFreak/ocman/internal/ocruntime"
	"github.com/NoUseFreak/ocman/internal/platforms"
	codexprovider "github.com/NoUseFreak/ocman/internal/platforms/codex/provider"
	"github.com/NoUseFreak/ocman/internal/platforms/provider"
	"github.com/NoUseFreak/ocman/internal/providerprotocol"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/tmux"
)

// supervisorIdentity is part of the launch identity: a supervisor launched
// with another CLI or an older backend is drained and replaced.
const supervisorIdentity = "codex-supervisor/v1"

// Config is what New needs from the application.
type Config struct {
	State   *state.DB
	DataDir string
	Version string // ocman's version, reported to Codex as its client
}

// New composes the Codex platform. Nothing is launched here: the supervisor
// starts on the first session operation, and one that survived an earlier
// dashboard is reattached in the background.
func New(ctx context.Context, cfg Config) platforms.Integration {
	p, err := newPlatform(ctx, cfg, launcher{})
	if err != nil {
		log.WithError(err).Warn("Codex sessions unavailable")
		return nil // a typed nil would register a broken platform
	}
	return p
}

// launcher resolves the processes the platform starts; tests replace it.
type launcher struct {
	exe string // ocman, launched as `ocman provider codex`
	cli string // codex
}

func newPlatform(ctx context.Context, cfg Config, l launcher) (*Platform, error) {
	if l.exe == "" {
		exe, err := os.Executable()
		if err != nil {
			return nil, err
		}
		l.exe = exe
	}
	if l.cli == "" {
		l.cli = "codex"
		if path, err := exec.LookPath(l.cli); err == nil {
			l.cli = path
		}
	}
	home, err := codexHome()
	if err != nil {
		return nil, fmt.Errorf("codex home unavailable: %w", err)
	}
	root := filepath.Join(cfg.DataDir, "codex-supervisor")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	// The key Ensure derives (symlinks resolved), so Recover finds its row.
	root, err = local.ProviderRoot(ctx, root)
	if err != nil {
		return nil, err
	}
	var rules *provider.Rules
	rules, err = provider.OpenRules(filepath.Join(cfg.DataDir, "codex-rules.json"))
	if err != nil {
		return nil, fmt.Errorf("permission rules unreadable: %w", err) // never start without the saved denies
	}
	ctx, stop := context.WithCancel(ctx)
	p := &Platform{ctx: ctx, stop: stop, cli: l.cli, root: root, home: home, rules: rules, history: newHistory(l.cli, cfg.Version, home)}
	runtime := ocruntime.NewBackendRuntime(codexprovider.PlatformID, l.exe, filepath.Join(cfg.DataDir, "p"))
	runtime.Args = func() []string {
		args := []string{"--codex-cli", l.cli, "--codex-home", home, "--ocman-version", cfg.Version}
		if addr, _ := p.mcpAddr.Load().(string); addr != "" {
			args = append(args, "--mcp-addr", addr)
		}
		return args
	}
	runtime.Identity = func() string {
		addr, _ := p.mcpAddr.Load().(string)
		return strings.Join([]string{l.cli, home, supervisorIdentity, addr}, "\x00")
	}
	p.mgr = &local.ProviderManager{Runtime: runtime, State: cfg.State, OnDrain: p.onDrain}
	if cfg.State != nil {
		p.wg.Go(p.recover)
	}
	return p, nil
}

// Platform is the Codex integration: the current supervisor's adapter,
// replaced when the supervisor is relaunched, plus the history source.
type Platform struct {
	ctx     context.Context
	stop    context.CancelFunc
	cli     string
	root    string
	home    string
	mgr     *local.ProviderManager
	rules   *provider.Rules
	history *history
	mcpAddr atomic.Value // string: ocman's bound MCP listener
	wg      sync.WaitGroup

	ensureMu sync.Mutex // one launch at a time

	mu       sync.Mutex
	adapter  *provider.Adapter
	endpoint string
	unwatch  context.CancelFunc // stops the current adapter's headless stream
	watch    context.Context    // the server's headless watch, once started
	handler  func(providerprotocol.Event)
	sink     platforms.SessionEventSink
	closed   bool
	draining bool
}

func (*Platform) ID() platforms.ID { return codexprovider.PlatformID }

func (*Platform) DisplayName() string { return platforms.DisplayName(codexprovider.PlatformID) }

// Capabilities are the supervisor's, known before one runs.
func (*Platform) Capabilities() platforms.Capabilities {
	caps := codexprovider.Capabilities().Capabilities
	caps.ExactPatterns = true
	return caps
}

// Available reports a usable Codex: the CLI and tmux to host its supervisor.
func (p *Platform) Available(context.Context) bool { return p.RuntimeAvailable() }

func (p *Platform) RuntimeAvailable() bool {
	_, err := exec.LookPath(p.cli)
	return err == nil && tmux.IsAvailable()
}

// SetMCPAddr replaces a surviving supervisor safely if its listener changed.
func (p *Platform) SetMCPAddr(addr string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.mcpAddr.Load() == addr {
		return
	}
	p.mcpAddr.Store(addr)
	if p.mgr.State != nil {
		p.wg.Go(p.recover)
	}
}

// EnsureRuntime starts the machine-wide supervisor; every directory shares it.
func (p *Platform) EnsureRuntime(ctx context.Context, dir string) (*hostsvc.EnsureRuntimeResult, error) {
	if !filepath.IsAbs(dir) {
		return nil, fmt.Errorf("codex directory must be absolute: %q", dir)
	}
	res, _, err := p.ensure(ctx)
	if err != nil {
		return nil, err
	}
	out := *res
	out.RepoRoot = dir
	return &out, nil
}

func (p *Platform) recover() {
	p.ensureMu.Lock()
	defer p.ensureMu.Unlock()
	ctx := p.ctx // pending approvals may take longer than a request timeout
	res, ok, err := p.mgr.Recover(ctx, p.root)
	if err != nil {
		log.WithError(err).Warn("codex: reattaching the supervisor")
		return
	}
	if ok {
		if _, err := p.attach(ctx, res.Endpoint); err != nil {
			log.WithError(err).Warn("codex: reattaching the supervisor")
		}
	}
}

// ensure launches or re-probes the supervisor and attaches to it.
func (p *Platform) ensure(ctx context.Context) (*hostsvc.EnsureRuntimeResult, *provider.Adapter, error) {
	p.ensureMu.Lock()
	defer p.ensureMu.Unlock()
	res, err := p.mgr.Ensure(ctx, p.root)
	if err != nil {
		return nil, nil, err
	}
	a, err := p.attach(ctx, res.Endpoint)
	return res, a, err
}

// attach connects to the supervisor at endpoint, replacing the adapter of
// an earlier one. A new adapter starts with empty caches (the replacement's
// sequence restarts) and gets the rules, sink, handler and headless stream.
func (p *Platform) attach(ctx context.Context, endpoint string) (*provider.Adapter, error) {
	return p.attachMode(ctx, endpoint, false, true)
}

func (p *Platform) attachMode(ctx context.Context, endpoint string, draining, compatible bool) (*provider.Adapter, error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, errors.New("codex platform closed")
	}
	if p.adapter != nil && p.endpoint == endpoint && p.draining == draining {
		a := p.adapter
		p.mu.Unlock()
		return a, nil
	}
	p.mu.Unlock()
	connect := provider.New
	if !compatible {
		connect = provider.NewDraining
	}
	a, err := connect(ctx, strings.TrimPrefix(endpoint, "unix://"))
	if err != nil {
		return nil, err
	}
	a.SetRules(p.rules)
	p.mu.Lock()
	defer p.mu.Unlock()
	switch {
	case p.closed:
		a.Close()
		return nil, errors.New("codex platform closed")
	case p.adapter != nil && p.endpoint == endpoint && p.draining == draining: // a concurrent attach won
		a.Close()
		return p.adapter, nil
	}
	a.SetSessionEventSink(p.sink)
	if p.handler != nil {
		a.SetEventHandler(p.handler)
	}
	old, unwatch := p.adapter, p.unwatch
	p.adapter, p.endpoint, p.unwatch = a, endpoint, nil
	p.draining = draining
	if unwatch != nil {
		unwatch()
	}
	if old != nil {
		old.Close()
	}
	p.startWatchLocked()
	return a, nil
}

func (p *Platform) startWatchLocked() {
	if p.watch == nil || p.adapter == nil {
		return
	}
	ctx, cancel := context.WithCancel(p.watch)
	a := p.adapter
	p.unwatch = cancel
	p.wg.Go(func() {
		if err := a.WatchEvents(ctx); err != nil && ctx.Err() == nil {
			log.WithError(err).Warn("codex: headless event stream ended")
		}
	})
}

// current is the attached adapter, nil before the supervisor first runs.
func (p *Platform) current() *provider.Adapter {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.adapter
}

// do runs fn on the supervisor, launching it when none is attached. A call
// that could not reach the supervisor relaunches one that has gone away, so
// the next call works. Only a refused dial, which never delivered the
// request, is retried: a request lost on an open connection may have started
// a turn before the supervisor died.
func (p *Platform) do(ctx context.Context, fn func(*provider.Adapter) error) error {
	p.mu.Lock()
	a, draining := p.adapter, p.draining
	inst := &ocruntime.Instance{ID: filepath.Base(filepath.Dir(strings.TrimPrefix(p.endpoint, "unix://")))}
	current := p.mgr.Runtime.Current(inst)
	p.mu.Unlock()
	if draining {
		return platforms.ErrBusy
	}
	if a == nil || !current {
		var err error
		if _, a, err = p.ensure(ctx); err != nil {
			return err
		}
	}
	err := fn(a)
	if !errors.Is(err, platforms.ErrPlatformUnreachable) {
		return err
	}
	_, next, ensureErr := p.ensure(ctx)
	if ensureErr != nil || !undelivered(err) {
		return err
	}
	return fn(next)
}

// onDrain attaches before the manager waits for active turns and prompts.
// A version-skewed supervisor exposes only version-independent routes.
func (p *Platform) onDrain(_, endpoint string, compatible bool) {
	if _, err := p.attachMode(p.ctx, endpoint, true, compatible); err != nil {
		log.WithError(err).Warn("codex: attaching a draining supervisor")
	}
}

// reply must not wait for replacement: the manager may be waiting for this
// very prompt to settle before it can finish draining.
func (p *Platform) reply(ctx context.Context, fn func(*provider.Adapter) error) error {
	if a := p.current(); a != nil {
		return fn(a)
	}
	return p.do(ctx, fn)
}

// undelivered reports a request that never reached a supervisor.
func undelivered(err error) bool {
	var op *net.OpError
	return errors.Is(err, platforms.ErrPlatformUnreachable) && errors.As(err, &op) && op.Op == "dial"
}

// Close stops the event streams and history reader; the supervisor keeps
// running for the next dashboard (AD-4).
func (p *Platform) Close() {
	p.mu.Lock()
	p.closed = true
	a := p.adapter
	p.adapter, p.endpoint = nil, ""
	if p.unwatch != nil {
		p.unwatch()
	}
	p.mu.Unlock()
	p.stop()
	p.wg.Wait()
	if a != nil {
		a.Close()
	}
	p.history.close()
}
