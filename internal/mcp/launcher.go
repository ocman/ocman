package mcp

import (
	"context"
	"fmt"
	"time"

	log "github.com/sirupsen/logrus"

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
	// Agent is the composer-level role for the first message (OpenCode:
	// "build", "plan", subagent name). Empty = platform default.
	Agent string
	// Reasoning is the model variant / thinking-budget for the first
	// message (e.g. "high", "max", "low"). Empty = platform default.
	Reasoning string
	// PermissionRules, when non-empty, replaces the child session's
	// permission ruleset immediately after creation.
	//
	// ponytail: no child<=parent subset check — the localhost caller
	// already controls the parent; correct glob/order-aware subset logic
	// lives in OpenCode, not here. Rules replace the ruleset outright.
	PermissionRules []platforms.PermissionRule
	// WorktreePath is the on-disk worktree path (empty for same-directory sessions).
	WorktreePath string
	// Branch is the git branch for the worktree (empty for same-directory sessions).
	Branch string
	// TmuxTarget is the tmux session or session:window used to launch
	// the child (empty for same-directory sessions when no tmux launch is needed).
	TmuxTarget string
	// WaitForResult connects this child turn to the calling MCP request.
	WaitForResult bool
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
	SetPermissionRules(ctx context.Context, req platforms.SetPermissionRulesRequest) error
	// PermissionRules reads a session's current ruleset, used to inherit
	// a parent's live YOLO/custom posture into a child at split time.
	PermissionRules(ctx context.Context, sessionID string) ([]platforms.PermissionRule, error)
}

// platformAdapter is the internal alias used within the package.
type platformAdapter = SessionClient

// WorktreeSessionRequest asks the host that owns ParentDir to create (or
// reuse) a worktree for Branch and open a session rooted at it.
type WorktreeSessionRequest struct {
	// ParentDir is any absolute path inside the project on the owning
	// host; the host resolves the repo root itself.
	ParentDir string
	Branch    string
	// NewBranch creates Branch off BaseRef instead of checking out an
	// existing branch.
	NewBranch bool
	// BaseRef is the base for a new branch. Empty lets the owning host
	// pick its default base ref.
	BaseRef string
}

// WorktreeSessionResult is what the owning host created.
type WorktreeSessionResult struct {
	SessionID    string
	WorktreePath string
	Branch       string
}

// WorktreeSessionCreator creates a worktree *and* its session on the host
// that owns the request's ParentDir. It is the mcp-side narrowing of
// hostsvc.Host.CreateWorktreeSession (mirroring ProjectOpencodeEnsurer),
// so this package never runs git itself: the server package injects an
// adapter over the owner-resolved Host (Router.ForDir), which for a
// remote-owned project creates the worktree on that machine (AD-16).
//
// Nil makes the worktree split fail closed — better a clear error than a
// worktree silently created on the wrong machine.
type WorktreeSessionCreator func(ctx context.Context, req WorktreeSessionRequest) (*WorktreeSessionResult, error)

// ProjectOpencodeEnsurer guarantees the project's single opencode
// instance is running for the given directory and returns its HTTP port.
// It mirrors hostsvc.Host.EnsureProjectOpencode, narrowed to (dir)->port
// so the mcp package stays decoupled from hostsvc. The server package
// injects an adapter over the owning host's EnsureProjectOpencode.
// Exported so the server package and tests can reference the type.
type ProjectOpencodeEnsurer func(ctx context.Context, dir string) (port string, err error)

// worktreeSessionCreator is the internal alias used within the package.
type worktreeSessionCreator = WorktreeSessionCreator

// projectOpencodeEnsurer is the internal alias used within the package.
type projectOpencodeEnsurer = ProjectOpencodeEnsurer

// SessionLauncher orchestrates the creation of child sessions.
type SessionLauncher struct {
	stateDB        childSessionStore
	platform       platformAdapter
	createWorktree worktreeSessionCreator
	ensureOpencode projectOpencodeEnsurer
	childResults   *ChildResultBroker
}

func (l *SessionLauncher) WithChildResults(results *ChildResultBroker) *SessionLauncher {
	l.childResults = results
	return l
}

// NewSessionLauncher creates a SessionLauncher with production dependencies.
// createWorktree and ensureOpencode are owner-routed host adapters
// injected by the server package (and faked in tests), so no host
// operation runs on the hub for a remote-owned project.
//
// ensureOpencode serves the same-directory split only: it launches the
// project's single opencode instance when none is running, so Launch
// creates the session in-app against the returned port instead of failing
// with ErrPlatformUnreachable (#268). The worktree path does not use it —
// there the owning host ensures its own instance inside
// CreateWorktreeSession, and ensuring again from here would target the
// worktree's repo top-level rather than the project root.
func NewSessionLauncher(
	stateDB childSessionStore,
	platform platformAdapter,
	createWorktree worktreeSessionCreator,
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
// Only the same-directory path reaches it: on the worktree path the
// owning host creates the session itself, and LaunchWithWorktree goes
// straight to AttachChild.
func (l *SessionLauncher) launchWithPort(ctx context.Context, req LaunchRequest, port string) (string, error) {
	// Create the OpenCode session.
	resp, err := l.platform.CreateSession(ctx, platforms.CreateSessionRequest{
		Directory: req.Directory,
		Port:      port,
	})
	if err != nil {
		return "", fmt.Errorf("creating child session: %w", err)
	}
	return resp.ID, l.AttachChild(ctx, req, resp.ID)
}

// AttachChild finishes a child launch for an already-created session:
// it registers the result waiter, applies the requested permission
// ruleset, sends the composed prompt and persists the child record.
// Split out so the worktree path — where the *owning host* creates the
// session (hostsvc.Host.CreateWorktreeSession) — shares one code path
// with the same-directory path.
func (l *SessionLauncher) AttachChild(ctx context.Context, req LaunchRequest, childID string) error {
	if req.ParentSessionID != "" && req.WaitForResult && l.childResults != nil {
		if !l.childResults.Register(childID) {
			return fmt.Errorf("child session %s already has a result waiter", childID)
		}
	}

	// Apply the requested permission ruleset before the first message so
	// it runs under those rules. Best-effort: the session exists; a
	// permission-set failure shouldn't strand it.
	if len(req.PermissionRules) > 0 {
		if err := l.platform.SetPermissionRules(ctx, platforms.SetPermissionRulesRequest{
			SessionID: childID,
			Rules:     req.PermissionRules,
		}); err != nil {
			log.WithFields(log.Fields{
				"childSessionID": childID,
				"error":          err,
			}).Warn("mcp: failed to set permission rules on child session")
		}
	}

	// Send the composed prompt as the first message.
	if req.ComposedPrompt != "" {
		if err := l.platform.SendMessage(ctx, platforms.SendMessageRequest{
			SessionID: childID,
			Message:   req.ComposedPrompt,
			Model:     req.Model,
			Agent:     req.Agent,
			Reasoning: req.Reasoning,
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
	}
	if req.ParentSessionID == "" {
		cs.ResultDelivery = "detached"
	} else if req.WaitForResult && l.childResults != nil {
		cs.ResultDelivery = "waiting"
	} else {
		cs.ResultDelivery = state.ChildResultAsyncPending
	}
	if err := l.stateDB.InsertChildSession(cs); err != nil {
		// Log but don't fail: the session is running; we just can't
		// track it in state.db.
		log.WithFields(log.Fields{
			"childSessionID": childID,
			"error":          err,
		}).Warn("mcp: failed to persist child session record")
	}

	return nil
}

// CreateWorktreeSession asks the host that owns req.ParentDir to create
// (or reuse) the worktree and open a session rooted at it. The owning
// host does the whole host-side job — repo-root resolution, `git worktree
// add`, ensuring the project's single opencode instance, creating the
// in-app session on it (#268) — so none of it runs on the hub for a
// remote-owned project. Fails closed when no host adapter was injected.
func (l *SessionLauncher) CreateWorktreeSession(ctx context.Context, req WorktreeSessionRequest) (*WorktreeSessionResult, error) {
	if l.createWorktree == nil {
		return nil, fmt.Errorf("worktree sessions are unavailable: no host adapter wired")
	}
	res, err := l.createWorktree(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("creating worktree session: %w", err)
	}
	if res == nil || res.SessionID == "" {
		return nil, fmt.Errorf("creating worktree session: host returned no session")
	}
	return res, nil
}

// LaunchWithWorktree creates the worktree session on the owning host and
// attaches the child (prompt, permission rules, state.db record). It
// returns the child session ID and the host's worktree result.
func (l *SessionLauncher) LaunchWithWorktree(
	ctx context.Context,
	req LaunchRequest,
	wtReq WorktreeSessionRequest,
) (childID string, wtResult *WorktreeSessionResult, err error) {
	wtResult, err = l.CreateWorktreeSession(ctx, wtReq)
	if err != nil {
		return "", nil, err
	}
	req.WorktreePath = wtResult.WorktreePath
	req.Branch = wtResult.Branch
	req.Directory = wtResult.WorktreePath
	return wtResult.SessionID, wtResult, l.AttachChild(ctx, req, wtResult.SessionID)
}
