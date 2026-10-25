package provider

import (
	"context"
	"errors"
	"net/url"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/providerprotocol"
)

func (a *Adapter) SendMessage(ctx context.Context, req platforms.SendMessageRequest) error {
	path := "/send"
	supported := a.Capabilities().Composer
	if req.Delivery == "queue" {
		path = "/hold"
		supported = supported && a.hello.Capabilities.NativeQueue
	}
	if req.Delivery != "" && req.Delivery != "queue" {
		return platforms.ErrUnsupported
	}
	return a.call(ctx, supported, "POST", sessionPath(req.SessionID, path), req, nil)
}

// Hook forwards a CLI hook event for a session started outside ocman.
func (a *Adapter) Hook(ctx context.Context, event providerprotocol.HookEvent) error {
	return a.call(ctx, a.hello.Capabilities.Hooks, "POST", "/hooks", event, nil)
}
func (a *Adapter) ExecuteCommand(ctx context.Context, req platforms.ExecuteCommandRequest) error {
	return a.call(ctx, a.Capabilities().SlashCommands, "POST", sessionPath(req.SessionID, "/command"), req, nil)
}
func (a *Adapter) RunShell(ctx context.Context, req platforms.RunShellRequest) error {
	return a.call(ctx, a.Capabilities().ShellExec, "POST", sessionPath(req.SessionID, "/shell"), req, nil)
}
func (a *Adapter) RespondPermission(ctx context.Context, req platforms.RespondPermissionRequest) error {
	// Read first: the provider's permission.replied can clear the cache
	// before the reply call returns.
	a.mu.RLock()
	prompt := a.permissions[req.PermissionID]
	a.mu.RUnlock()
	// "Allow always" is persisted before the reply: when it cannot be, the
	// prompt stays pending and answerable instead of being acknowledged.
	// It grants nothing until the outcome is known: success, or the
	// provider's permission.replied carrying the reply claude received
	// (another, earlier decision may stand).
	if req.Reply == "always" {
		if err := a.rememberAlways(prompt); err != nil {
			return err
		}
	}
	err := a.call(ctx, a.Capabilities().RespondPermission, "POST", "/permissions/"+url.PathEscape(req.PermissionID)+"/reply", req, nil)
	if err == nil {
		// claude has the reply: a store failure here is settled from the
		// provider's permission.replied, which is replayed until it is.
		_ = a.settleAlways(req.PermissionID, req.Reply == "always")
		a.mu.Lock()
		delete(a.permissions, req.PermissionID)
		a.promptVersion++
		a.mu.Unlock()
	}
	return err
}
func (a *Adapter) RespondQuestion(ctx context.Context, req platforms.RespondQuestionRequest) error {
	err := a.call(ctx, a.Capabilities().RespondQuestion, "POST", "/questions/"+url.PathEscape(req.RequestID)+"/reply", req, nil)
	if err == nil {
		a.mu.Lock()
		delete(a.questions, req.RequestID)
		a.promptVersion++
		a.mu.Unlock()
	}
	return err
}
func (a *Adapter) RejectQuestion(ctx context.Context, req platforms.RejectQuestionRequest) error {
	err := a.call(ctx, a.Capabilities().RespondQuestion, "POST", "/questions/"+url.PathEscape(req.RequestID)+"/reject", req, nil)
	if err == nil {
		a.mu.Lock()
		delete(a.questions, req.RequestID)
		a.promptVersion++
		a.mu.Unlock()
	}
	return err
}
func (a *Adapter) Abort(ctx context.Context, req platforms.AbortRequest) error {
	if req.TaskID != "" && !a.Capabilities().BackgroundTaskStop {
		return platforms.ErrUnsupported
	}
	return a.call(ctx, a.Capabilities().Abort, "POST", sessionPath(req.SessionID, "/abort"), req, nil)
}
func (a *Adapter) RenameSession(ctx context.Context, req platforms.RenameSessionRequest) error {
	if req.OnlyIfUntitled {
		return a.call(ctx, a.Capabilities().AutoTitle, "POST", sessionPath(req.SessionID, "/auto-title"), req, nil)
	}
	return a.call(ctx, a.hello.Capabilities.Rename, "POST", sessionPath(req.SessionID, "/rename"), req, nil)
}
func (a *Adapter) Compact(ctx context.Context, req platforms.CompactRequest) error {
	return a.call(ctx, a.Capabilities().Compact, "POST", sessionPath(req.SessionID, "/compact"), req, nil)
}
func (a *Adapter) PermissionRules(ctx context.Context, id string) ([]platforms.PermissionRule, error) {
	if r := a.localRules(); r != nil {
		return r.get(id), nil
	}
	var rules []platforms.PermissionRule
	err := a.call(ctx, a.Capabilities().PermissionRules, "GET", sessionPath(id, "/rules"), nil, &rules)
	return rules, err
}
func (a *Adapter) SetPermissionRules(ctx context.Context, req platforms.SetPermissionRulesRequest) error {
	if req.NativeMode != nil {
		return a.call(ctx, a.Capabilities().NativeAutoPermission, "POST", sessionPath(req.SessionID, "/native-permission"), req, nil)
	}
	if r := a.localRules(); r != nil {
		return r.set(req.SessionID, req.Rules)
	}
	return a.call(ctx, a.Capabilities().PermissionRules, "POST", sessionPath(req.SessionID, "/rules"), req, nil)
}

// DisposeSession lets configured creates (routines, inherited worktree
// permissions) run on providers. The protocol has no delete, so a session
// whose ruleset failed to apply stays, empty, and the caller reports it.
func (a *Adapter) DisposeSession(context.Context, platforms.DisposeSessionRequest) error {
	return errors.New("provider sessions cannot be deleted; the empty session was left in place")
}

func (a *Adapter) MoveSession(ctx context.Context, req platforms.MoveSessionRequest) error {
	err := a.call(ctx, a.Capabilities().Move, "POST", sessionPath(req.SessionID, "/move"), req, nil)
	if err == nil {
		a.mu.Lock()
		a.known[req.SessionID] = true
		a.mu.Unlock()
	}
	return err
}
func (a *Adapter) create(ctx context.Context, supported bool, path string, req any) (*platforms.CreateSessionResponse, error) {
	if !supported {
		return nil, errors.Join(platforms.ErrCreationRejected, platforms.ErrUnsupported)
	}
	var result platforms.CreateSessionResponse
	if err := a.call(ctx, supported, "POST", path, req, &result); err != nil {
		return nil, err
	}
	if result.ID == "" {
		return nil, platforms.ErrNotFound
	}
	s := db.Session{ID: result.ID}
	if r, ok := req.(platforms.CreateSessionRequest); ok {
		s.Directory = r.Directory
	}
	a.remember(s)
	return &result, nil
}
func (a *Adapter) CreateSession(ctx context.Context, req platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
	return a.create(ctx, a.Capabilities().Composer, "/sessions", req)
}
func (a *Adapter) ForkSession(ctx context.Context, req platforms.ForkSessionRequest) (*platforms.CreateSessionResponse, error) {
	return a.create(ctx, a.Capabilities().Fork, sessionPath(req.SessionID, "/fork"), req)
}
func (a *Adapter) NativeQueued(ctx context.Context, id string) ([]platforms.NativeQueuedMessage, error) {
	var messages []platforms.NativeQueuedMessage
	err := a.call(ctx, a.hello.Capabilities.NativeQueue, "GET", sessionPath(id, "/held"), nil, &messages)
	return messages, err
}
func (a *Adapter) CancelNativeQueued(ctx context.Context, req platforms.CancelNativeQueuedRequest) error {
	return a.call(ctx, a.hello.Capabilities.NativeQueue, "POST", sessionPath(req.SessionID, "/held/"+url.PathEscape(req.ID)+"/cancel"), req, nil)
}

// LiveSessions lists the statuses the provider's own process settled
// (GET /sessions?live=1): id, status and resetsAt only.
func (a *Adapter) LiveSessions(ctx context.Context) ([]db.Session, error) {
	var sessions []db.Session
	err := a.call(ctx, true, "GET", "/sessions?live=1", nil, &sessions)
	if err == nil {
		for _, session := range sessions {
			a.remember(session)
		}
	}
	return sessions, err
}

// RuntimeSessions enumerates sessions owned by the supervisor, including ones
// without transcripts. Status-only observations of old helpers are not an
// ingestion inventory. Older supervisors fall back to their ordinary session
// list for this unknown query parameter, which also excludes those helpers.
func (a *Adapter) RuntimeSessions(ctx context.Context) ([]db.Session, error) {
	var sessions []db.Session
	err := a.call(ctx, true, "GET", "/sessions?runtime=1", nil, &sessions)
	if err == nil {
		for _, session := range sessions {
			a.remember(session)
		}
	}
	return sessions, err
}
