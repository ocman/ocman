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

const (
	// portPollInterval is how often we check for the child OpenCode
	// instance's port after launching it in a git.
	portPollInterval = 500 * time.Millisecond

	// portPollTimeout is the maximum time we wait for the child
	// OpenCode instance to bind its port after tmux launch.
	portPollTimeout = 10 * time.Second
)

// LaunchRequest describes a child session to create.
type LaunchRequest struct {
	// ParentSessionID is the ID of the session that triggered the split.
	ParentSessionID string
	// Platform is the platform ID (e.g. "opencode").
	Platform string
	// Directory is the working directory for the new session.
	// For split_to_session this is the parent's cwd.
	// For split_to_worktree this is the worktree path.
	Directory string
	// Intent is the caller-provided sub-task description.
	Intent string
	// ComposedPrompt is the enriched prompt to send as the first message.
	ComposedPrompt string
	// Model is the optional platform model reference used for the first message.
	Model string
	// WorktreePath is the on-disk worktree path (empty for split_to_session).
	WorktreePath string
	// Branch is the git branch for the worktree (empty for split_to_session).
	Branch string
	// TmuxTarget is the tmux session or session:window used to launch
	// the child (empty for split_to_session when no tmux launch is needed).
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

// TmuxLauncher abstracts the tmux launch helpers for testing.
// Exported so the server package and tests can reference the type.
type TmuxLauncher func(projectDir, worktreeDir string) (target string, launched bool, err error)

// PortDiscoverer returns the HTTP port for a running OpenCode instance
// in the given directory, or "" if none is found.
// Exported so the server package and tests can reference the type.
type PortDiscoverer func(directory string) string

// worktreeCreator is the internal alias used within the package.
type worktreeCreator = WorktreeCreator

// tmuxLauncher is the internal alias used within the package.
type tmuxLauncher = TmuxLauncher

// portDiscoverer is the internal alias used within the package.
type portDiscoverer = PortDiscoverer

// SessionLauncher orchestrates the creation of child sessions.
type SessionLauncher struct {
	stateDB        childSessionStore
	platform       platformAdapter
	createWorktree worktreeCreator
	launchTmux     tmuxLauncher
	discoverPort   portDiscoverer
}

// NewSessionLauncher creates a SessionLauncher with production dependencies.
// The worktreeCreator, tmuxLauncher, and portDiscoverer are injected so
// tests can substitute fakes without requiring real git/tmux/opencode.
func NewSessionLauncher(
	stateDB childSessionStore,
	platform platformAdapter,
	createWorktree worktreeCreator,
	launchTmux tmuxLauncher,
	discoverPort portDiscoverer,
) *SessionLauncher {
	return &SessionLauncher{
		stateDB:        stateDB,
		platform:       platform,
		createWorktree: createWorktree,
		launchTmux:     launchTmux,
		discoverPort:   discoverPort,
	}
}

// Launch creates a child session and persists it to state.db.
// It calls Platform.CreateSession to create the session, then
// Platform.SendMessage to deliver the composed prompt.
//
// For worktree splits, the worktree and tmux session must already be
// set up before calling Launch (the caller is responsible for that).
// Launch only handles the OpenCode session creation and prompt delivery.
func (l *SessionLauncher) Launch(ctx context.Context, req LaunchRequest) (string, error) {
	// Create the OpenCode session.
	resp, err := l.platform.CreateSession(ctx, platforms.CreateSessionRequest{
		Directory: req.Directory,
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

// LaunchWithWorktree creates a git worktree, launches OpenCode in it via
// tmux, waits for the port to become discoverable, then calls Launch.
// It returns the child session ID and the worktree creation result.
func (l *SessionLauncher) LaunchWithWorktree(
	ctx context.Context,
	req LaunchRequest,
	wtReq git.CreateWorktreeRequest,
) (childID string, wtResult *git.CreateWorktreeResult, err error) {
	// Create (or reuse) the git.
	wtResult, err = l.createWorktree(ctx, wtReq)
	if err != nil {
		return "", nil, fmt.Errorf("creating worktree: %w", err)
	}

	// Launch OpenCode in the worktree via tmux.
	tmuxTarget, _, err := l.launchTmux(wtReq.RepoRoot, wtResult.Path)
	if err != nil {
		return "", wtResult, fmt.Errorf("launching opencode in tmux: %w", err)
	}
	req.TmuxTarget = tmuxTarget
	req.WorktreePath = wtResult.Path
	req.Branch = wtResult.Branch
	req.Directory = wtResult.Path

	// Poll for the OpenCode port to become available.
	if err := l.waitForPort(ctx, wtResult.Path); err != nil {
		return "", wtResult, fmt.Errorf("waiting for opencode port: %w", err)
	}

	childID, err = l.Launch(ctx, req)
	return childID, wtResult, err
}

// waitForPort polls discoverPort until a port is found for the given
// directory or the timeout is reached.
func (l *SessionLauncher) waitForPort(ctx context.Context, directory string) error {
	deadline := time.Now().Add(portPollTimeout)
	for {
		if port := l.discoverPort(directory); port != "" {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for opencode to start in %s", directory)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(portPollInterval):
		}
	}
}
