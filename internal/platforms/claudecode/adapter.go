// Package claudecode implements the platforms.Platform interface for
// Claude Code (Anthropic's local CLI).
//
// Session data lives as append-only JSONL files at
// ~/.claude/projects/<dir-encoded>/<session-uuid>.jsonl. This adapter
// reads them lazily with an in-memory cache keyed by (path, mtime,
// size); see AD-3 in the multi-agent architecture spec.
//
// Phase 4 ships read-only support (browse, archive, mark-seen,
// auto-archive). Composer, permission/question responses, abort, and
// compact all return ErrUnsupported — they come in Phase 5+.
package claudecode

import (
	"context"
	"io"
	"os"
	"path/filepath"

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
}

// New returns a Claude Code adapter reading from the user's default
// projects directory. Returns an adapter whose Available() is false
// when $HOME can't be determined — ocman must keep running without
// Claude Code if the user hasn't installed it.
func New() *Adapter {
	home, err := os.UserHomeDir()
	if err != nil {
		return &Adapter{} // Available() will report false.
	}
	return NewFromDir(filepath.Join(home, defaultProjectsDir))
}

// NewFromDir returns a Claude Code adapter rooted at the given
// directory. Mostly useful for tests; production code calls New.
func NewFromDir(projectsDir string) *Adapter {
	return &Adapter{
		projectsDir: projectsDir,
		cache:       newCache(),
	}
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

// Capabilities reflects Phase 4's read-only slice. Later phases will
// flip composer/respondPermission/respondQuestion/abort/compact/events
// to true as each ships.
func (a *Adapter) Capabilities() platforms.Capabilities {
	return platforms.Capabilities{
		// Read-only capabilities (supported in Phase 4).
		// Nothing here yet — none of the flags describe "we can
		// show you the session", which is always true if the
		// adapter is Available.

		// All interactive capabilities come in later phases.
		Composer:          false,
		RespondPermission: false,
		RespondQuestion:   false,
		Abort:             false,
		Compact:           false,
		Events:            false,
		AgentCatalog:      false,
		ModelCatalog:      false,
		SlashCommands:     false,
	}
}

// --- Interactive operations: not supported in Phase 4 ---

func (a *Adapter) SendMessage(context.Context, platforms.SendMessageRequest) error {
	return platforms.ErrUnsupported
}

func (a *Adapter) ExecuteCommand(context.Context, platforms.ExecuteCommandRequest) error {
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

// LiveStatus returns nil in Phase 4 — live-state is driven by hooks
// which land in Phase 5.
func (a *Adapter) LiveStatus(string) *platforms.LiveState { return nil }

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
