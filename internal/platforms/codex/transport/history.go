package codextransport

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	codexappserver "github.com/NoUseFreak/ocman/internal/platforms/codex/appserver"
	codexprovider "github.com/NoUseFreak/ocman/internal/platforms/codex/provider"
	"github.com/NoUseFreak/ocman/internal/providerprotocol"
)

// history reads Codex's thread store through its own app-server, separate
// from the supervisor's: stored threads are readable without loading them,
// so these reads never resume a thread or start a turn. The process starts
// on first use and again after it exits.
type history struct {
	start func(context.Context) (*codexappserver.Client, error)

	mu       sync.Mutex
	client   *codexappserver.Client
	models   cached[*platforms.DirectoryCatalog]
	commands map[string]cached[[]platforms.SlashCommandEntry] // by directory
}

// cached is a value read at a time, served until catalogTTL has passed.
type cached[T any] struct {
	value T
	at    time.Time
}

func (c cached[T]) fresh() bool { return !c.at.IsZero() && time.Since(c.at) < catalogTTL }

func newHistory(cli, version, home string) *history {
	return &history{start: func(ctx context.Context) (*codexappserver.Client, error) {
		// No handler: server requests are refused, notifications ignored.
		return codexappserver.Start(ctx, codexappserver.Options{CLI: cli, Env: append(os.Environ(), "CODEX_HOME="+home),
			Client: codexappserver.ClientInfo{Name: "ocman", Title: "ocman history", Version: version}}, nil)
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
	c, err := h.start(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: starting codex app-server: %w", platforms.ErrHistoryUnavailable, err)
	}
	h.client = c
	return c, nil
}

func (h *history) close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.client != nil {
		_ = h.client.Close()
		h.client = nil
	}
}

// reader binds a HistoryReader to the current client.
func (h *history) reader(ctx context.Context) (*codexprovider.HistoryReader, error) {
	c, err := h.conn(ctx)
	if err != nil {
		return nil, err
	}
	return &codexprovider.HistoryReader{Client: c}, nil
}

// sessions lists stored threads, archived ones included.
func (h *history) sessions(ctx context.Context, dir string, since int64) ([]db.Session, error) {
	c, err := h.conn(ctx)
	if err != nil {
		return nil, err
	}
	out := []db.Session{}
	for _, archived := range []bool{false, true} {
		threads, err := c.ListThreads(ctx, codexappserver.ThreadListParams{SourceKinds: codexappserver.AllSourceKinds, Archived: &archived, Cwd: dir})
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
			list, err := c.ListModels(ctx, false)
			if err != nil {
				return nil, err
			}
			models = cached[*platforms.DirectoryCatalog]{codexprovider.Catalog(list), time.Now()}
		}
		if req.Directory != "" && !commands.fresh() {
			skills, err := c.ListSkills(ctx, req.Directory)
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
	return &out, nil
}

// Account reports the signed-in Codex account and its usage windows for
// Settings, through the history app-server. Usage windows are best effort:
// the plan still shows when they cannot be read.
func (p *Platform) Account(ctx context.Context) (*providerprotocol.Account, error) {
	c, err := p.history.conn(ctx)
	if err != nil {
		return nil, err
	}
	account, err := c.ReadAccount(ctx)
	if err != nil {
		return nil, err
	}
	limits, err := c.ReadRateLimits(ctx)
	if err != nil {
		limits = nil
	}
	return codexprovider.Account(account, limits)
}
