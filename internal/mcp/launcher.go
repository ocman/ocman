package mcp

import (
	"context"
	"fmt"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/git"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

// LaunchRequest describes a child session to create.
type LaunchRequest struct {
	// ParentSessionID is the ID of the session that triggered the split.
	ParentSessionID string
	// Platform is the platform ID (e.g. "opencode").
	Platform string
	// Directory is the working directory for the new session.
	// For a same-directory new_session this is the parent's cwd.
	// For a worktree new_session this is the worktree path.
	Directory string
	// Intent is the caller-provided sub-task description.
	Intent string
	// ComposedPrompt is the enriched prompt to send as the first message.
	ComposedPrompt string
	// Model is the optional platform model reference used for the first message.
	Model string
	// WorktreePath is the on-disk worktree path (empty for same-directory sessions).
	WorktreePath string
	// Branch is the git branch for the worktree (empty for same-directory sessions).
	Branch string
	// TmuxTarget is the tmux session or session:window used to launch
	// the child (empty for same-directory sessions when no tmux launch is needed).
	TmuxTarget string
	// LoopID links this child to an agent loop (empty for one-shot
	// splits). Persisted on the child_sessions row so the loop engine
	// can track and aggregate the child (AD-5).
	LoopID string
}

// childSessionStore is the subset of state.DB used by SessionLauncher.
type childSessionStore interface {
	InsertChildSession(cs state.ChildSession) error
}

// SessionClient is the narrow session-mutation surface used by
// SessionLauncher and the comm tools. In production this is a
// sessionsvc.Client bound to a platform id, so MCP-initiated creates
// and messages share the same validated code path (and hooks) as the
// REST handlers and the remote gRPC server.
type SessionClient interface {
	CreateSession(ctx context.Context, req platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error)
	SendMessage(ctx context.Context, req platforms.SendMessageRequest) error
}

// platformAdapter is the internal alias used within the package.
type platformAdapter = SessionClient

// WorktreeCreator abstracts git.CreateWorktree for testing.
// Exported so the server package and tests can reference the type.
type WorktreeCreator func(ctx context.Context, req git.CreateWorktreeRequest) (*git.CreateWorktreeResult, error)

// ProjectOpencodeEnsurer guarantees the project's single opencode
// instance is running for the given directory and returns its HTTP port.
// It mirrors hostsvc.Host.EnsureProjectOpencode, narrowed to (dir)->port
// so the mcp package stays decoupled from hostsvc. The server package
// injects an adapter over the owning host's EnsureProjectOpencode.
// Exported so the server package and tests can reference the type.
type ProjectOpencodeEnsurer func(ctx context.Context, dir string) (port string, err error)

// worktreeCreator is the internal alias used within the package.
type worktreeCreator = WorktreeCreator

// projectOpencodeEnsurer is the internal alias used within the package.
type projectOpencodeEnsurer = ProjectOpencodeEnsurer

// SessionLauncher orchestrates the creation of child sessions.
type SessionLauncher struct {
	stateDB        childSessionStore
	platform       platformAdapter
	createWorktree worktreeCreator
	ensureOpencode projectOpencodeEnsurer
}

// NewSessionLauncher creates a SessionLauncher with production dependencies.
// createWorktree and ensureOpencode are injected so tests can substitute
// fakes without requiring real git/opencode. ensureOpencode makes both
// same-directory and worktree splits self-heal: it launches the project's
// single opencode instance when none is running, then Launch creates the
// session in-app against the returned port (#268).
func NewSessionLauncher(
	stateDB childSessionStore,
	platform platformAdapter,
	createWorktree worktreeCreator,
	ensureOpencode projectOpencodeEnsurer,
) *SessionLauncher {
	return &SessionLauncher{
		stateDB:        stateDB,
		platform:       platform,
		createWorktree: createWorktree,
		ensureOpencode: ensureOpencode,
	}
}

// Launch creates a child session and persists it to state.db.
// It ensures the project's single opencode instance is running for
// req.Directory (launching it if none — so same-directory splits
// self-heal instead of returning ErrPlatformUnreachable), then calls
// Platform.CreateSession against the ensured port and Platform.SendMessage
// to deliver the composed prompt.
func (l *SessionLauncher) Launch(ctx context.Context, req LaunchRequest) (string, error) {
	// Ensure the project's opencode instance is running and thread its
	// port into CreateSession (skips lsof discovery).
	//
	// Best-effort by design (spec D-2 / US-10): a same-directory split
	// should *self-heal* — launch the instance when none is running — but
	// must never fail *harder* than before this feature. If ensuring
	// fails (e.g. tmux absent on the host), fall through with an empty
	// port so CreateSession's own discovery still finds an
	// already-running instance; it surfaces ErrPlatformUnreachable itself
	// if there genuinely is none.
	port := ""
	if l.ensureOpencode != nil && req.Directory != "" {
		if p, err := l.ensureOpencode(ctx, req.Directory); err != nil {
			log.WithFields(log.Fields{
				"directory": req.Directory,
				"error":     err,
			}).Warn("mcp: ensure project opencode failed; falling back to discovery")
		} else {
			port = p
		}
	}
	return l.launchWithPort(ctx, req, port)
}

// launchWithPort creates the session on the given port (empty = let the
// adapter discover), sends the prompt, and persists the child record.
// It is the shared body of Launch and LaunchWithWorktree; the latter has
// already ensured the project instance against the repo root, so it
// passes that port instead of re-ensuring on the worktree path (which
// resolves to a different repo top-level).
func (l *SessionLauncher) launchWithPort(ctx context.Context, req LaunchRequest, port string) (string, error) {
	// Create the OpenCode session.
	resp, err := l.platform.CreateSession(ctx, platforms.CreateSessionRequest{
		Directory: req.Directory,
		Port:      port,
	})
	if err != nil {
		return "", fmt.Errorf("creating child session: %w", err)
	}
	childID := resp.ID

	// Send the composed prompt as the first message.
	if req.ComposedPrompt != "" {
		if err := l.platform.SendMessage(ctx, platforms.SendMessageRequest{
			SessionID: childID,
			Message:   req.ComposedPrompt,
			Model:     req.Model,
		}); err != nil {
			// Log but don't fail: the session was created; the user can
			// still interact with it manually.
			log.WithFields(log.Fields{
				"childSessionID": childID,
				"error":          err,
			}).Warn("mcp: failed to send composed prompt to child session")
		}
	}

	// Persist the child session record.
	cs := state.ChildSession{
		ID:              childID,
		Platform:        req.Platform,
		ParentSessionID: req.ParentSessionID,
		Intent:          req.Intent,
		ComposedPrompt:  req.ComposedPrompt,
		WorktreePath:    req.WorktreePath,
		Branch:          req.Branch,
		TmuxTarget:      req.TmuxTarget,
		Status:          "starting",
		CreatedAt:       time.Now().UnixMilli(),
		LoopID:          req.LoopID,
	}
	if err := l.stateDB.InsertChildSession(cs); err != nil {
		// Log but don't fail: the session is running; we just can't
		// track it in state.db.
		log.WithFields(log.Fields{
			"childSessionID": childID,
			"error":          err,
		}).Warn("mcp: failed to persist child session record")
	}

	return childID, nil
}

// LaunchWithWorktree creates a git worktree, ensures the project's single
// opencode instance is running (rooted at the repo's main checkout), then
// creates an in-app session rooted at the worktree on that instance's
// port (#268 — no per-worktree tmux window). It returns the child session
// ID and the worktree creation result.
func (l *SessionLauncher) LaunchWithWorktree(
	ctx context.Context,
	req LaunchRequest,
	wtReq git.CreateWorktreeRequest,
) (childID string, wtResult *git.CreateWorktreeResult, err error) {
	// Create (or reuse) the worktree.
	wtResult, err = l.createWorktree(ctx, wtReq)
	if err != nil {
		return "", nil, fmt.Errorf("creating worktree: %w", err)
	}

	req.WorktreePath = wtResult.Path
	req.Branch = wtResult.Branch
	req.Directory = wtResult.Path

	// Ensure the project's single opencode instance against the repo's
	// main checkout (NOT the worktree path, whose git top-level differs),
	// then create the session in-app on that port.
	port := ""
	if l.ensureOpencode != nil {
		p, ensErr := l.ensureOpencode(ctx, wtReq.RepoRoot)
		if ensErr != nil {
			return "", wtResult, fmt.Errorf("ensuring project opencode: %w", ensErr)
		}
		port = p
	}

	childID, err = l.launchWithPort(ctx, req, port)
	return childID, wtResult, err
}
