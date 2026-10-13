package claudeprovider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/providerprotocol"
	"github.com/NoUseFreak/ocman/internal/providersupervisor"
)

// PlatformID is the platform id the provider advertises in /hello.
const PlatformID = "claudecode"

type Options struct {
	IndexPath string        // claude-index.db, beside state.db
	ConfigDir string        // ~/.claude or an inherited CLAUDE_CONFIG_DIR
	CLI       string        // claude executable
	Sweep     time.Duration // mtime sweep interval, the watcher's backstop
	StatePath string        // sessions ocman launched; empty keeps them in memory
	UsagePath string        // rate-limit windows observed by every supervisor; empty records none
	// Pool policy, owned by ocman (AD-3, spike §1): one machine-wide cap,
	// idle timeout counted from turn completion.
	MaxProcs    int
	IdleTimeout time.Duration
	MCPConfig   string // --mcp-config for ocman's MCP server; empty omits it
	// LaunchPath is the dashboard's current launch configuration
	// (WriteLaunchConfig); its MCP address wins over MCPConfig, so a
	// supervisor that outlives a dashboard restart follows a rebound
	// listener. Empty uses MCPConfig only.
	LaunchPath string
	// ConfigEnv is the CLAUDE_CONFIG_DIR claude children get, carried
	// explicitly from the dashboard (an empty value unsets it); nil inherits
	// this process's environment.
	ConfigEnv *string
	PluginDir string // --plugin-dir with ocman's skills; empty omits it
	// SlotDir holds the machine-wide child slots shared by every supervisor;
	// empty caps this supervisor alone.
	SlotDir string
	// NoSync leaves claude-index.db to the browsing supervisor: a project
	// supervisor reads it but never rescans the transcripts itself.
	NoSync bool
}

// Spike-measured defaults: ~400 MiB per child, cache survives a resume.
const (
	DefaultMaxProcs    = 4
	DefaultIdleTimeout = 10 * time.Minute
)

// DefaultOptions places the index in dataDir and reads the active Claude
// config directory.
func DefaultOptions(dataDir string) Options {
	config := ConfigDir(os.Getenv("CLAUDE_CONFIG_DIR"))
	return Options{IndexPath: filepath.Join(dataDir, "claude-index.db"), ConfigDir: config, CLI: "claude", Sweep: 5 * time.Second,
		StatePath: filepath.Join(dataDir, "claude-sessions.json"), UsagePath: filepath.Join(dataDir, "claude-usage.json"), LaunchPath: filepath.Join(dataDir, "claude-launch.json"), MaxProcs: DefaultMaxProcs, IdleTimeout: DefaultIdleTimeout,
		SlotDir: filepath.Join(dataDir, "claude-slots")}
}

// ConfigDir is the Claude config directory for a CLAUDE_CONFIG_DIR value.
func ConfigDir(env string) string {
	if env != "" {
		return env
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude")
}

// NewSupervisor opens the index, keeps it synced until ctx ends, serves
// session browsing for every transcript and drives the sessions ocman
// launches over the provider protocol.
func NewSupervisor(ctx context.Context, opts Options, spool string) (*providersupervisor.Supervisor, error) {
	sup, _, err := newSupervisor(ctx, opts, spool)
	return sup, err
}

func newSupervisor(ctx context.Context, opts Options, spool string) (*providersupervisor.Supervisor, *live, error) {
	if opts.MaxProcs <= 0 {
		opts.MaxProcs = DefaultMaxProcs
	}
	if opts.IdleTimeout <= 0 {
		opts.IdleTimeout = DefaultIdleTimeout
	}
	idx, err := OpenIndex(opts.IndexPath, opts.ConfigDir)
	if err != nil {
		return nil, nil, err
	}
	version := cliVersion(ctx, opts.CLI)
	l := newLive(idx, opts, version)
	sup, err := providersupervisor.New(spool, providersupervisor.Backend{
		Hello: providerprotocol.Hello{
			ProtocolVersion: providerprotocol.CurrentVersion,
			ProviderID:      PlatformID,
			CLIVersion:      version,
			Capabilities: providerprotocol.Capabilities{Rename: true, Account: true, Hooks: true, Capabilities: platforms.Capabilities{
				Composer: true, Abort: true, Compact: true, Fork: true, Events: true, SlashCommands: true, ModelCatalog: true, AgentCatalog: true,
				RespondPermission: true, RespondQuestion: true, PermissionRules: true,
				// ocman's judge decides Claude prompts too (FR-5); no revert (FR-8).
				AutoApprove: true, PushPrompts: true}},
		},
		Handler:  &handler{idx: idx, live: l, account: l.account},
		Snapshot: func(_ context.Context, id string) ([]providerprotocol.Event, error) { return l.snapshot(id), nil },
	})
	if err != nil {
		_ = idx.Close()
		return nil, nil, err
	}
	l.sup = sup
	go l.yieldSlots(ctx)
	go func() {
		if opts.NoSync {
			<-ctx.Done()
		} else {
			idx.Run(ctx, opts.Sweep)
		}
		l.stopAll()
		_ = idx.Close()
	}()
	return sup, l, nil
}

// cliVersion reports the installed claude version without requiring one:
// browsing transcripts works with the CLI absent.
func cliVersion(ctx context.Context, cli string) string {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second) // a first exec can be slow (macOS binary scan)
	defer cancel()
	out, err := exec.CommandContext(ctx, cli, "--version").Output()
	if fields := strings.Fields(string(out)); err == nil && len(fields) > 0 {
		return fields[0]
	}
	return "unavailable"
}

// Run syncs on start, on directory events and on every sweep tick.
func (x *Index) Run(ctx context.Context, sweep time.Duration) {
	if sweep <= 0 {
		sweep = 5 * time.Second
	}
	wake := make(chan struct{}, 1)
	if w, err := newDirWatcher(func() {
		select {
		case wake <- struct{}{}:
		default:
		}
	}); err == nil {
		x.mu.Lock()
		x.watch = w.Add
		x.mu.Unlock()
		defer w.Close()
	}
	ticker := time.NewTicker(sweep)
	defer ticker.Stop()
	for {
		if err := x.Sync(ctx); err != nil && ctx.Err() == nil {
			log.WithError(err).Warn("syncing claude transcript index")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-wake:
			// Coalesce a burst of writes into one sync.
			select {
			case <-ctx.Done():
				return
			case <-time.After(200 * time.Millisecond):
			}
		}
	}
}

type handler struct {
	idx     *Index
	live    *live
	account *account
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if r.Method != http.MethodGet {
		h.live.serve(w, r)
		return
	}
	var v any
	var err error
	switch {
	case path == "/sessions":
		v, err = h.list(r)
	case path == "/permissions" || path == "/questions":
		v = h.live.pendingPrompts(r.URL.Query().Get("sessionID"), path == "/questions")
	case path == "/catalog":
		v = h.live.catalog(r.Context(), r.URL.Query().Get("directory"))
	case path == "/account" && h.account != nil:
		v, err = h.account.get(r.Context())
	case strings.HasPrefix(path, "/sessions/") && strings.HasSuffix(path, "/lifecycle"):
		v, err = h.lifecycle(r.Context(), strings.TrimSuffix(strings.TrimPrefix(path, "/sessions/"), "/lifecycle"))
	case strings.HasPrefix(path, "/sessions/") && !strings.Contains(strings.TrimPrefix(path, "/sessions/"), "/"):
		v, err = h.detail(r)
	default:
		writeJSON(w, http.StatusNotImplemented, providerprotocol.Error{Code: "unsupported", Message: "unsupported operation"})
		return
	}
	switch {
	case errors.Is(err, platforms.ErrNotFound):
		writeJSON(w, http.StatusNotFound, providerprotocol.Error{Code: "not_found", Message: "not found"})
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, providerprotocol.Error{Message: err.Error()})
	default:
		writeJSON(w, http.StatusOK, v)
	}
}

func (h *handler) list(r *http.Request) ([]db.Session, error) {
	if r.URL.Query().Get("live") != "" {
		return h.live.statuses(), nil
	}
	where, args := "1 = 1", []any{}
	if dir := r.URL.Query().Get("directory"); dir != "" {
		where, args = where+" AND directory = ?", append(args, dir)
	}
	if since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64); since > 0 {
		where, args = where+" AND time_updated >= ?", append(args, since)
	}
	entries, err := h.idx.query(r.Context(), where+" ORDER BY time_updated DESC", args...)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	sessions := []db.Session{}
	indexed := map[string]bool{}
	for _, e := range entries {
		indexed[e.ID] = true
		s := e.session(now)
		// Like OpenCode, finished subagents stay out of the list; they
		// remain reachable through their parent's session tree.
		if s.ParentID != "" && s.Status != db.StatusBusy {
			continue
		}
		h.live.overlay(&s)
		sessions = append(sessions, s)
	}
	return append(sessions, h.live.pending(indexed, r.URL.Query().Get("directory"))...), nil
}

func (h *handler) entry(ctx context.Context, id string) (*entry, error) {
	e, err := h.idx.get(ctx, id)
	if err == nil && e != nil {
		e, err = h.idx.refresh(ctx, e)
	}
	if err == nil && e == nil {
		err = platforms.ErrNotFound
	}
	return e, err
}

func (h *handler) lifecycle(ctx context.Context, id string) (*platforms.SessionLifecycle, error) {
	e, err := h.entry(ctx, id)
	if s, ok := h.unindexed(id, err); ok {
		return &platforms.SessionLifecycle{Status: s.Status, ReadOnly: !s.LiveConnection}, nil
	}
	if err != nil {
		return nil, err
	}
	s := e.session(time.Now())
	h.live.overlay(&s)
	return &platforms.SessionLifecycle{Status: s.Status, ReadOnly: !s.LiveConnection, LatestMessageID: e.LatestID,
		LatestMessageCreated: e.LatestCreated, LatestMessageRole: e.LatestRole}, nil
}

// unindexed is an owned session claude has not written a transcript for yet.
func (h *handler) unindexed(id string, err error) (db.Session, bool) {
	if !errors.Is(err, platforms.ErrNotFound) {
		return db.Session{}, false
	}
	for _, s := range h.live.pending(nil, "") {
		if s.ID == id {
			return s, true
		}
	}
	return db.Session{}, false
}

// detail pages like db.PaginateMessages (offset counts from the newest
// message) and reads only the transcript bytes of the requested page.
func (h *handler) detail(r *http.Request) (*platforms.SessionDetail, error) {
	ctx := r.Context()
	id := strings.TrimPrefix(r.URL.Path, "/sessions/")
	e, err := h.entry(ctx, id)
	if s, ok := h.unindexed(id, err); ok {
		return &platforms.SessionDetail{Session: &s, Messages: []db.Message{}, Parts: []db.Part{}, SessionTree: []db.Session{s}}, nil
	}
	if err != nil {
		return nil, err
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	offs, err := h.idx.offsets(ctx, e.ID)
	if err != nil {
		return nil, err
	}
	total := len(offs)
	start, end := max(total-offset-limit, 0), min(max(total-offset, 0), total)
	children, err := h.idx.children(ctx, e.ID)
	if err != nil {
		return nil, err
	}
	c := &Converter{SessionID: e.ID, Children: children, Messages: []db.Message{}, Parts: []db.Part{}}
	if start < end {
		to := e.End
		if end < total {
			to = offs[end]
		}
		if err := h.idx.convert(e.Path, offs[start], to, c); err != nil {
			return nil, err
		}
	}
	tree, err := h.idx.query(ctx, "root_id = ? ORDER BY time_created, id", e.RootID)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	detail := &platforms.SessionDetail{Messages: c.Messages, Parts: c.Parts, TotalMessages: total, DefaultModel: e.Model}
	s := e.session(now)
	h.live.overlay(&s)
	detail.Session = &s
	for _, t := range tree {
		detail.SessionTree = append(detail.SessionTree, t.session(now))
	}
	return detail, nil
}
