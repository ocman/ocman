// Package claudecode implements the platforms.Platform interface for
// Claude Code (Anthropic's local CLI).
//
// Session data lives as append-only JSONL files at
// ~/.claude/projects/<dir-encoded>/<session-uuid>.jsonl. This adapter
// reads them lazily with an in-memory cache keyed by (path, mtime,
// size); see AD-3 in the multi-agent architecture spec.
//
// Phase 4 ships read-only support (browse, archive, mark-seen,
// auto-archive). Phase 5 adds live-state via Claude Code hooks — the
// adapter holds an in-memory cache that's mutated by hook events
// posted to /api/hooks/claude, and Sessions() / Session() overlay
// that cache onto the static jsonl-derived state. Composer,
// permission/question responses, abort, and compact still return
// ErrUnsupported — those come in Phase 6+.
package claudecode

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

// PlatformID is the stable identifier used in URLs, state.db rows, and
// JSON wire payloads.
const PlatformID platforms.ID = "claude-code"

// defaultProjectsDir is the relative path under $HOME where Claude Code
// keeps its session JSONLs.
const defaultProjectsDir = ".claude/projects"

// Adapter implements platforms.Platform for Claude Code.
type Adapter struct {
	// projectsDir is the absolute path to `~/.claude/projects`. Set
	// once at construction so tests can point at a fixture directory
	// without stubbing $HOME.
	projectsDir string

	// cache memoises parse results keyed by (path, mtime, size).
	cache *cache

	// live holds hook-driven live-state (status, pending permission)
	// for each session, keyed by session UUID. In-memory, goroutine-
	// safe; see live_cache.go for details. Never nil after
	// construction — New / NewFromDir initialise it so handler code
	// can dispatch without nil checks.
	live *liveCache

	// sender spawns the claude subprocess for composer messages.
	// Defaults to execSpawner{}; overridable by tests.
	sender spawner
}

// New returns a Claude Code adapter reading from the user's default
// projects directory. Returns an adapter whose Available() is false
// when $HOME can't be determined — ocman must keep running without
// Claude Code if the user hasn't installed it.
func New() *Adapter {
	home, err := os.UserHomeDir()
	if err != nil {
		// Even an unavailable adapter needs an initialised live cache
		// so that handler code can call ApplyHookEvent without first
		// checking whether the adapter found its projects dir.
		return &Adapter{live: newLiveCache(defaultBusyTTL)}
	}
	return NewFromDir(filepath.Join(home, defaultProjectsDir))
}

// NewFromDir returns a Claude Code adapter rooted at the given
// directory. Mostly useful for tests; production code calls New.
func NewFromDir(projectsDir string) *Adapter {
	return &Adapter{
		projectsDir: projectsDir,
		cache:       newCache(),
		live:        newLiveCache(defaultBusyTTL),
		sender:      &execSpawner{},
	}
}

// WithSender overrides the default subprocess spawner; used by tests
// to swap in a fake. Returns the adapter for chaining.
func (a *Adapter) WithSender(s spawner) *Adapter {
	a.sender = s
	return a
}

// ID returns the Claude Code platform identifier.
func (a *Adapter) ID() platforms.ID { return PlatformID }

// DisplayName returns the user-facing name.
func (a *Adapter) DisplayName() string { return "Claude Code" }

// Available reports whether Claude Code's projects directory exists.
// Returns false when the user doesn't have Claude Code installed or
// hasn't started any sessions yet — in that case the adapter stays
// registered but silent, and the UI hides it via the capabilities
// endpoint.
func (a *Adapter) Available(context.Context) bool {
	if a.projectsDir == "" {
		return false
	}
	info, err := os.Stat(a.projectsDir)
	return err == nil && info.IsDir()
}

// Capabilities declares what Claude Code supports.
//
// Composer is true from Phase 6 on — SendMessage routes via
// `claude -p --resume`. Per-session availability is implied by
// session.LiveConnection (set when a hook event has been observed);
// the frontend gates the composer UI on that flag.
//
// Other interactive ops stay false: Claude Code has no in-flight
// permission/question response API, no compact primitive, and its
// SSE live-event stream isn't proxied through ocman.
func (a *Adapter) Capabilities() platforms.Capabilities {
	return platforms.Capabilities{
		Composer:          true,
		RespondPermission: false,
		RespondQuestion:   false,
		Abort:             false,
		Compact:           false,
		Events:            false,
		AgentCatalog:      false,
		ModelCatalog:      false,
		SlashCommands:     false,
		FileChanges:       false,
		SessionInfo:       false,
	}
}

// SessionChanges is unsupported for Claude Code: per-edit tool input
// captures old_string/new_string but no surrounding-context
// filediff snapshot, so a parity feature with OpenCode isn't
// possible without reading files from disk (which may have changed
// since the edit). Return ErrUnsupported so handlers serve a
// Supported=false payload.
func (a *Adapter) SessionChanges(context.Context, string) (*platforms.SessionChanges, error) {
	return nil, platforms.ErrUnsupported
}

// SessionInfo is unsupported for Claude Code: the platform doesn't
// expose a structured MCP / LSP catalog or per-session context-window
// metadata over a stable interface. Return ErrUnsupported so handlers
// serve a Supported=false payload (the frontend hides the info pane
// for this platform via the SessionInfo capability flag, but the
// graceful fallback keeps the wire shape identical across platforms).
func (a *Adapter) SessionInfo(context.Context, string) (*platforms.SessionInfo, error) {
	return nil, platforms.ErrUnsupported
}

// --- Interactive operations ---

// SendMessage spawns `claude -p --resume <id> "<message>"` in the
// session's working directory. Fire-and-forget: the subprocess runs
// detached, appends new turns to its jsonl file, and fires hook
// events back into ocman as it runs. The read path picks up the new
// messages on the next Sessions()/Session() poll.
//
// Returns an error only if the session can't be resolved or the
// subprocess fails to start. Runtime errors from the claude process
// itself (bad prompt, API failure) show up via the jsonl's error
// fields, not this return value.
//
// Images are not supported — claude -p doesn't accept inline
// attachments — so req.Images is silently ignored. Phase 6 scope.
func (a *Adapter) SendMessage(ctx context.Context, req platforms.SendMessageRequest) error {
	if req.SessionID == "" {
		return fmt.Errorf("claudecode SendMessage: SessionID required")
	}
	// AD-13: refuse to send while the target session is busy. A
	// concurrent `claude -p --resume` against an in-flight TUI turn
	// forks the conversation tree (see
	// spec/multi-agent-support/phase7/findings.md). `busy` is the
	// only blocked state: `done`, `error`, and "never observed"
	// (nil live state) all pass. The stale-busy TTL in liveCache
	// prevents a dead session from blocking the composer forever.
	if a.live != nil {
		if st := a.live.Get(req.SessionID); st != nil && st.Status == "busy" {
			return platforms.ErrBusy
		}
	}
	// Look up the session to recover its cwd. Required because
	// claude -p runs in cwd and uses it to resolve project-scoped
	// settings (CLAUDE.md, plugins, etc).
	detail, err := a.Session(ctx, req.SessionID, 1, 0)
	if err != nil {
		return fmt.Errorf("claudecode SendMessage: resolve session: %w", err)
	}
	if detail.Session == nil || detail.Session.Directory == "" {
		return fmt.Errorf("claudecode SendMessage: session %s has no directory", req.SessionID)
	}
	return sendPromptWith(ctx, a.sender, req.SessionID, detail.Session.Directory, req.Message)
}

func (a *Adapter) ExecuteCommand(context.Context, platforms.ExecuteCommandRequest) error {
	return platforms.ErrUnsupported
}

// RunShell is unsupported on Claude Code: the CLI has no equivalent
// of OpenCode's /shell primitive, so `!`-prefixed input falls back
// to a normal prompt via the composer's caps.shellExec gate.
func (a *Adapter) RunShell(context.Context, platforms.RunShellRequest) error {
	return platforms.ErrUnsupported
}

func (a *Adapter) RespondPermission(context.Context, platforms.RespondPermissionRequest) error {
	return platforms.ErrUnsupported
}

func (a *Adapter) RespondQuestion(context.Context, platforms.RespondQuestionRequest) error {
	return platforms.ErrUnsupported
}

func (a *Adapter) RejectQuestion(context.Context, platforms.RejectQuestionRequest) error {
	return platforms.ErrUnsupported
}

func (a *Adapter) Abort(context.Context, platforms.AbortRequest) error {
	return platforms.ErrUnsupported
}

func (a *Adapter) RenameSession(context.Context, platforms.RenameSessionRequest) error {
	return platforms.ErrUnsupported
}

func (a *Adapter) Compact(context.Context, platforms.CompactRequest) error {
	return platforms.ErrUnsupported
}

func (a *Adapter) CreateSession(context.Context, platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
	return nil, platforms.ErrUnsupported
}

func (a *Adapter) ProxyEvents(context.Context, string, io.Writer, func()) error {
	return platforms.ErrUnsupported
}

// --- Catalog operations: no concept of composer agents / commands / models ---

func (a *Adapter) AgentCatalog(context.Context, string) ([]platforms.AgentCatalogEntry, error) {
	return nil, nil
}

func (a *Adapter) SlashCommands(context.Context, string) ([]platforms.SlashCommandEntry, error) {
	return nil, nil
}

func (a *Adapter) SessionModels(context.Context, string) (*platforms.SessionModelsResponse, error) {
	return &platforms.SessionModelsResponse{Models: []platforms.SessionModel{}}, nil
}

func (a *Adapter) ListPermissions(context.Context, string) ([]platforms.LivePrompt, error) {
	return nil, nil
}

func (a *Adapter) ListQuestions(context.Context, string) ([]platforms.LivePrompt, error) {
	return nil, nil
}

// LiveStatus returns the cache entry for sessionID, or nil if no
// hook event has ever been seen for this session. The returned
// pointer is a copy — callers may mutate it safely.
func (a *Adapter) LiveStatus(sessionID string) *platforms.LiveState {
	if a.live == nil {
		return nil
	}
	return a.live.Get(sessionID)
}

// RefreshHooks rewrites the user's ~/.claude/settings.json so that
// Claude Code POSTs every managed hook event to hookURL. Called from
// server boot; safe to call repeatedly — see InstallHooks for the
// merge/idempotence semantics.
//
// Separated from InstallHooks so callers don't need to know about
// $HOME or the filename convention; tests can still exercise the
// underlying installer directly.
//
// Returns a non-nil error if $HOME can't be resolved or the installer
// fails. Callers (typically the server's boot path) should log the
// error and continue — a missing hook install degrades to read-only
// Claude Code support, not a hard failure.
func (a *Adapter) RefreshHooks(hookURL string) error {
	if a.projectsDir == "" {
		// Adapter was constructed with no home dir; nothing to
		// install into. Shouldn't reach here — callers gate this
		// with Available() — but be defensive.
		return fmt.Errorf("claudecode: adapter has no projects directory configured")
	}
	// The settings file lives next to the projects directory at
	// $HOME/.claude/settings.json, so derive it from projectsDir
	// rather than re-reading $HOME. Keeps the adapter self-contained
	// and makes tests that pass a custom projectsDir work transparently.
	settingsPath := filepath.Join(filepath.Dir(a.projectsDir), "settings.json")
	return InstallHooks(settingsPath, hookURL)
}

// ApplyHookEvent decodes a Claude Code hook payload and updates the
// live-state cache. Called by the /api/hooks/claude handler.
//
// Errors (malformed JSON) are returned so the handler can log with
// context; Ignored events (unknown event_name, empty session_id)
// return nil so the hook command exits 0 from the CLI's perspective.
//
// The context is accepted for future use (persisted audit trail,
// metrics, etc.) — Phase 5 only consults wall-clock time via the
// cache.
func (a *Adapter) ApplyHookEvent(_ context.Context, payload []byte) error {
	if a.live == nil {
		return fmt.Errorf("claudecode: adapter has no live cache")
	}
	ev, err := parseHookPayload(payload, time.Now())
	if err != nil {
		return err
	}
	if ev.Ignored {
		return nil
	}
	a.live.Apply(ev.SessionID, ev.toLiveStateDelta())
	return nil
}

// SessionsInactiveBefore returns an empty slice until the adapter
// populates live-state; stale Claude Code sessions will still be
// surfaced for auto-archive once Sessions() is fully wired in later
// steps of this phase.
func (a *Adapter) SessionsInactiveBefore(ctx context.Context, cutoff int64) ([]db.SessionArchiveCandidate, error) {
	sessions, err := a.Sessions(ctx, "", 0)
	if err != nil {
		return nil, err
	}
	out := make([]db.SessionArchiveCandidate, 0)
	for _, s := range sessions {
		if s.TimeUpdated < cutoff {
			out = append(out, db.SessionArchiveCandidate{
				ID:          s.ID,
				TimeUpdated: s.TimeUpdated,
			})
		}
	}
	return out, nil
}
