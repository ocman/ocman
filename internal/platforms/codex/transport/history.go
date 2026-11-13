package codextransport

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	codexappserver "github.com/ocman/ocman/internal/platforms/codex/appserver"
	codexprovider "github.com/ocman/ocman/internal/platforms/codex/provider"
	"github.com/ocman/ocman/internal/providerprotocol"
)

// history reads Codex's thread store through its own app-server, separate
// from the supervisor's: stored threads are readable without loading them,
// so these reads never resume a thread or start a turn. The process starts
// on first use and again after it exits.
type history struct {
	start func(context.Context) (*codexappserver.Client, error)

	mu           sync.Mutex
	client       *codexappserver.Client
	readerClient *codexappserver.Client
	readerCache  *codexprovider.HistoryReader
	models       cached[*platforms.DirectoryCatalog]
	commands     map[string]cached[[]platforms.SlashCommandEntry] // by directory
}

// cached is a value read at a time, served until catalogTTL has passed.
type cached[T any] struct {
	value T
	at    time.Time
}

// historyTimeout bounds each history app-server call, like the supervisor's
// rpcTimeout. The JSON-RPC client has no deadline of its own, so a wedged
// app-server would otherwise block every reader indefinitely.
var historyTimeout = 30 * time.Second

func (c cached[T]) fresh() bool { return !c.at.IsZero() && time.Since(c.at) < catalogTTL }

func newHistory(cli, version, home string, named bool) *history {
	return &history{start: func(ctx context.Context) (*codexappserver.Client, error) {
		env := append(os.Environ(), "CODEX_HOME="+home)
		if named {
			env = connectionEnvironment(os.Environ(), home)
		}
		return codexappserver.Start(ctx, codexappserver.Options{CLI: cli, Env: env,
			Client: codexappserver.ClientInfo{Name: "ocman", Title: "ocman history", Version: version}})
	}}
}

func (h *history) conn(ctx context.Context) (*codexappserver.Client, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.client != nil {
		select {
		case <-h.client.Done():
			_ = h.client.Close()
			h.client = nil
		default:
			return h.client, nil
		}
	}
	start, cancel := context.WithTimeout(ctx, historyTimeout)
	defer cancel()
	c, err := h.start(start)
	if err != nil {
		return nil, fmt.Errorf("%w: starting codex app-server: %w", platforms.ErrHistoryUnavailable, err)
	}
	h.client = c
	return c, nil
}

func (h *history) close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.readerCache, h.readerClient = nil, nil
	if h.client != nil {
		_ = h.client.Close()
		h.client = nil
	}
}

// timed runs one app-server call within historyTimeout. A call that hit that
// timeout, rather than the caller's own deadline, marks the app-server
// unhealthy: it is closed so the next use starts a new one.
func timed[T any](ctx context.Context, h *history, c *codexappserver.Client, call func(context.Context) (T, error)) (T, error) {
	bounded, cancel := context.WithTimeout(ctx, historyTimeout)
	defer cancel()
	out, err := call(bounded)
	if err != nil && ctx.Err() == nil && errors.Is(bounded.Err(), context.DeadlineExceeded) {
		h.mu.Lock()
		if h.client == c {
			_ = c.Close()
			h.client = nil
		}
		h.mu.Unlock()
	}
	return out, err
}

// timedClient bounds every HistoryReader call on one app-server client.
type timedClient struct {
	h *history
	c *codexappserver.Client
}

func (t timedClient) ListThreads(ctx context.Context, p codexappserver.ThreadListParams) ([]codexappserver.Thread, error) {
	return timed(ctx, t.h, t.c, func(ctx context.Context) ([]codexappserver.Thread, error) { return t.c.ListThreads(ctx, p) })
}

func (t timedClient) ReadThread(ctx context.Context, id string, turns bool) (*codexappserver.Thread, error) {
	return timed(ctx, t.h, t.c, func(ctx context.Context) (*codexappserver.Thread, error) { return t.c.ReadThread(ctx, id, turns) })
}

func (t timedClient) ListTurns(ctx context.Context, threadID, itemsView string) ([]codexappserver.Turn, error) {
	return timed(ctx, t.h, t.c, func(ctx context.Context) ([]codexappserver.Turn, error) {
		return t.c.ListTurns(ctx, threadID, itemsView)
	})
}

func (t timedClient) ListItems(ctx context.Context, threadID string) ([]codexappserver.ItemEntry, error) {
	return timed(ctx, t.h, t.c, func(ctx context.Context) ([]codexappserver.ItemEntry, error) { return t.c.ListItems(ctx, threadID) })
}

// reader binds a HistoryReader to the current client.
func (h *history) reader(ctx context.Context) (*codexprovider.HistoryReader, error) {
	c, err := h.conn(ctx)
	if err != nil {
		return nil, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.readerCache == nil || h.readerClient != c {
		h.readerClient, h.readerCache = c, &codexprovider.HistoryReader{Client: timedClient{h, c}}
	}
	return h.readerCache, nil
}

// sessions lists stored threads, archived ones included.
func (h *history) sessions(ctx context.Context, dir string, since int64) ([]db.Session, error) {
	c, err := h.conn(ctx)
	if err != nil {
		return nil, err
	}
	reader := timedClient{h, c}
	out := []db.Session{}
	for _, archived := range []bool{false, true} {
		threads, err := reader.ListThreads(ctx, codexappserver.ThreadListParams{SourceKinds: codexappserver.AllSourceKinds, Archived: &archived, Cwd: dir})
		if err != nil {
			return nil, err
		}
		for i := range threads {
			if s := codexprovider.Session(&threads[i]); !threads[i].Ephemeral && s.TimeUpdated >= since {
				out = append(out, s)
			}
		}
	}
	return out, nil
}

// codexHome is the thread store Codex uses: CODEX_HOME, else ~/.codex.
func codexHome() (string, error) {
	home := os.Getenv("CODEX_HOME")
	if home == "" {
		user, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		home = filepath.Join(user, ".codex")
	}
	home, err := filepath.Abs(home)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(home)
}

// HistoryIdentity names the physical store: one per Codex home.
func (p *Platform) HistoryIdentity(context.Context) (platforms.HistoryIdentity, error) {
	return platforms.HistoryIdentity{Platform: codexprovider.PlatformID, SourceKey: p.home, Name: "Codex"}, nil
}

func (p *Platform) HistoryInventory(ctx context.Context) ([]db.HistoryEntry, error) {
	r, err := p.history.reader(ctx)
	if err != nil {
		return nil, err
	}
	return r.Inventory(ctx)
}

func (p *Platform) HistorySnapshot(ctx context.Context, id string) (*db.HistorySnapshot, error) {
	r, err := p.history.reader(ctx)
	if err != nil {
		return nil, err
	}
	return r.Snapshot(ctx, id)
}

// SessionIdentityMetadata proves a thread belongs to this Codex home before
// it is bound, from metadata alone.
func (p *Platform) SessionIdentityMetadata(ctx context.Context, id string) (*db.Session, error) {
	r, err := p.history.reader(ctx)
	if err != nil {
		return nil, err
	}
	return r.SessionIdentityMetadata(ctx, id)
}

// catalogTTL bounds how stale the model list may be; model/list may reach
// the network.
const catalogTTL = 10 * time.Minute

// LiveDirectoryCatalog lists Codex's models and the directory's commands
// for the composer. Models are machine-wide and cached once; commands
// (built-ins and the directory's skills) per directory. It is read through
// the history app-server: opening the composer never launches a supervisor.
func (p *Platform) LiveDirectoryCatalog(ctx context.Context, req platforms.DirectoryCatalogRequest) (*platforms.DirectoryCatalog, error) {
	h := p.history
	h.mu.Lock()
	models, commands := h.models, h.commands[req.Directory]
	h.mu.Unlock()
	if !models.fresh() || (req.Directory != "" && !commands.fresh()) {
		c, err := h.conn(ctx)
		if err != nil {
			return nil, err
		}
		if !models.fresh() {
			list, err := timed(ctx, h, c, func(ctx context.Context) ([]codexappserver.Model, error) { return c.ListModels(ctx, false) })
			if err != nil {
				return nil, err
			}
			models = cached[*platforms.DirectoryCatalog]{codexprovider.Catalog(list), time.Now()}
		}
		if req.Directory != "" && !commands.fresh() {
			skills, err := timed(ctx, h, c, func(ctx context.Context) ([]codexappserver.SkillsEntry, error) {
				return c.ListSkills(ctx, req.Directory)
			})
			if err != nil {
				return nil, err
			}
			commands = cached[[]platforms.SlashCommandEntry]{codexprovider.Commands(skills), time.Now()}
		}
		h.mu.Lock()
		h.models = models
		if req.Directory != "" {
			if h.commands == nil {
				h.commands = map[string]cached[[]platforms.SlashCommandEntry]{}
			}
			h.commands[req.Directory] = commands
		}
		h.mu.Unlock()
	}
	out := *models.value
	out.Commands = commands.value
	if out.Commands == nil {
		out.Commands = codexprovider.Commands(nil)
	}
	// Read only the existing supervisor's cached ACP announcements. Catalog
	// browsing must never launch or resume an execution connection.
	if adapter := p.current(); adapter != nil {
		catalog, err := adapter.LiveDirectoryCatalog(ctx, req)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// A dead or older execution supervisor must not break native model
		// and skill browsing, or cause a replacement launch from this read.
		if err == nil && catalog != nil && catalog.Commands != nil {
			out.Commands = mergeAdvertisedCommands(out.Commands, catalog.Commands)
		}
	}
	return &out, nil
}

func mergeAdvertisedCommands(base, advertised []platforms.SlashCommandEntry) []platforms.SlashCommandEntry {
	// The adapter's list is authoritative for built-ins. Keep the read-only
	// skill catalog's classification and skills absent from the announcement.
	skills := map[string]platforms.SlashCommandEntry{}
	for _, command := range base {
		if command.Source == "skill" {
			skills[command.Name] = command
		}
	}
	out := make([]platforms.SlashCommandEntry, 0, len(advertised)+len(skills))
	for _, command := range advertised {
		if skill, ok := skills[command.Name]; ok {
			command.Source = "skill"
			if command.Description == "" {
				command.Description = skill.Description
			}
		}
		delete(skills, command.Name)
		out = append(out, command)
	}
	for _, command := range base {
		if _, ok := skills[command.Name]; ok {
			out = append(out, command)
		}
	}
	return out
}

// Account reports the signed-in Codex account and its usage windows for
// Settings, through the history app-server. Usage windows are best effort:
// the plan still shows when they cannot be read.
func (p *Platform) Account(ctx context.Context) (*providerprotocol.Account, error) {
	c, err := p.history.conn(ctx)
	if err != nil {
		return nil, err
	}
	account, err := timed(ctx, p.history, c, c.ReadAccount)
	if err != nil {
		return nil, err
	}
	limits, err := timed(ctx, p.history, c, c.ReadRateLimits)
	if err != nil {
		limits = nil
	}
	return codexprovider.Account(account, limits)
}
