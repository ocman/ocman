package server

import (
	"context"
	"io"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

// fakePlatform is a minimal Platform implementation for server-package
// integration tests. Tests wire it into a Registry alongside the real
// OpenCode adapter to exercise cross-platform handler paths (list merge
// ordering, capability fan-out, state.db scoping) without needing a
// second real adapter with its own on-disk data store.
//
// The zero value is usable: id defaults to "fake", Available returns
// true, and every method returns a zero/empty result. Tests override
// only the fields they care about (typically id + sessions).
type fakePlatform struct {
	id           string
	displayName  string
	available    *bool // nil = treat as true; set to override
	caps         platforms.Capabilities
	sessions     []db.Session
	sessionsErr  error
	inactive     []db.SessionArchiveCandidate
	inactiveErr  error
	liveStatus   *platforms.LiveState
	sessionsHook func(ctx context.Context, dir string, since int64) ([]db.Session, error)
	changes      *platforms.SessionChanges
	changesErr   error
	info         *platforms.SessionInfo
	infoErr      error
	// runShell, when non-nil, intercepts RunShell calls — used by
	// the /api/session/{id}/shell handler test to record the
	// adapter received the call.
	runShell func() error
	// sendMessageFn, when non-nil, intercepts SendMessage calls.
	sendMessageFn func(req platforms.SendMessageRequest) error
	// createSessionFn, when non-nil, intercepts CreateSession calls
	// (used by the PR/Issue sidebar "handle" tests to verify a
	// session is launched without spinning up a real OpenCode).
	createSessionFn func(req platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error)
	// sessionDetailFn, when non-nil, intercepts Session calls.
	sessionDetailFn func(id string) (*platforms.SessionDetail, error)
	// proxyEventsFn, when non-nil, intercepts ProxyEvents calls so
	// SSE-handler tests can drive both the success path (write some
	// bytes, return nil) and the unreachable path (return
	// ErrPlatformUnreachable without writing).
	proxyEventsFn func(ctx context.Context, sessionID string, w io.Writer, flush func()) error
	// respondPermissionFn, when non-nil, intercepts RespondPermission
	// calls — used by the auto-approve cache-hit test to verify the
	// background pipeline invoked the adapter to clear a pending
	// permission without ever calling the LLM judge.
	respondPermissionFn func(req platforms.RespondPermissionRequest) error
	// listPermissionsFn, when non-nil, intercepts ListPermissions
	// calls — used by the auto-approve enable-toggle test to drive a
	// pending prompt through the resume-on-enable path without
	// needing a real OpenCode instance.
	listPermissionsFn func(sessionID string) ([]platforms.LivePrompt, error)
	// permissionRulesFn / setPermissionRulesFn, when non-nil,
	// intercept the per-session permission-ruleset read/write.
	permissionRulesFn    func(sessionID string) ([]platforms.PermissionRule, error)
	setPermissionRulesFn func(req platforms.SetPermissionRulesRequest) error
}

func (f *fakePlatform) ID() platforms.ID {
	if f.id == "" {
		return "fake"
	}
	return platforms.ID(f.id)
}

func (f *fakePlatform) DisplayName() string {
	if f.displayName == "" {
		return string(f.ID())
	}
	return f.displayName
}

func (f *fakePlatform) Available(context.Context) bool {
	if f.available == nil {
		return true
	}
	return *f.available
}

func (f *fakePlatform) Capabilities() platforms.Capabilities { return f.caps }

func (f *fakePlatform) Sessions(ctx context.Context, dir string, since int64) ([]db.Session, error) {
	if f.sessionsHook != nil {
		return f.sessionsHook(ctx, dir, since)
	}
	return f.sessions, f.sessionsErr
}

func (f *fakePlatform) Session(_ context.Context, id string, _, _ int) (*platforms.SessionDetail, error) {
	if f.sessionDetailFn != nil {
		return f.sessionDetailFn(id)
	}
	return nil, platforms.ErrNotFound
}

func (f *fakePlatform) Owns(_ context.Context, sessionID string) bool {
	for _, s := range f.sessions {
		if s.ID == sessionID {
			return true
		}
	}
	return false
}

func (f *fakePlatform) SessionsInactiveBefore(context.Context, int64) ([]db.SessionArchiveCandidate, error) {
	return f.inactive, f.inactiveErr
}

func (f *fakePlatform) SessionChanges(context.Context, string) (*platforms.SessionChanges, error) {
	if f.changes != nil {
		return f.changes, f.changesErr
	}
	return nil, platforms.ErrUnsupported
}

func (f *fakePlatform) SessionInfo(context.Context, string) (*platforms.SessionInfo, error) {
	if f.info != nil || f.infoErr != nil {
		return f.info, f.infoErr
	}
	return nil, platforms.ErrUnsupported
}

func (f *fakePlatform) LiveStatus(string) *platforms.LiveState { return f.liveStatus }

func (f *fakePlatform) AgentCatalog(context.Context, string) ([]platforms.AgentCatalogEntry, error) {
	return nil, nil
}

func (f *fakePlatform) SlashCommands(context.Context, string) ([]platforms.SlashCommandEntry, error) {
	return nil, nil
}

func (f *fakePlatform) SessionModels(context.Context, string) (*platforms.SessionModelsResponse, error) {
	return nil, nil
}

func (f *fakePlatform) ListPermissions(_ context.Context, sessionID string) ([]platforms.LivePrompt, error) {
	if f.listPermissionsFn != nil {
		return f.listPermissionsFn(sessionID)
	}
	return nil, nil
}

func (f *fakePlatform) ListQuestions(context.Context, string) ([]platforms.LivePrompt, error) {
	return nil, nil
}

func (f *fakePlatform) SendMessage(_ context.Context, req platforms.SendMessageRequest) error {
	if f.sendMessageFn != nil {
		return f.sendMessageFn(req)
	}
	return platforms.ErrUnsupported
}

func (f *fakePlatform) ExecuteCommand(context.Context, platforms.ExecuteCommandRequest) error {
	return platforms.ErrUnsupported
}

func (f *fakePlatform) RunShell(context.Context, platforms.RunShellRequest) error {
	if f.runShell != nil {
		return f.runShell()
	}
	return platforms.ErrUnsupported
}

func (f *fakePlatform) RespondPermission(_ context.Context, req platforms.RespondPermissionRequest) error {
	if f.respondPermissionFn != nil {
		return f.respondPermissionFn(req)
	}
	return platforms.ErrUnsupported
}

func (f *fakePlatform) RespondQuestion(context.Context, platforms.RespondQuestionRequest) error {
	return platforms.ErrUnsupported
}

func (f *fakePlatform) RejectQuestion(context.Context, platforms.RejectQuestionRequest) error {
	return platforms.ErrUnsupported
}

func (f *fakePlatform) Abort(context.Context, platforms.AbortRequest) error {
	return platforms.ErrUnsupported
}

func (f *fakePlatform) RenameSession(context.Context, platforms.RenameSessionRequest) error {
	return platforms.ErrUnsupported
}

func (f *fakePlatform) PermissionRules(_ context.Context, sessionID string) ([]platforms.PermissionRule, error) {
	if f.permissionRulesFn != nil {
		return f.permissionRulesFn(sessionID)
	}
	return nil, platforms.ErrUnsupported
}

func (f *fakePlatform) SetPermissionRules(_ context.Context, req platforms.SetPermissionRulesRequest) error {
	if f.setPermissionRulesFn != nil {
		return f.setPermissionRulesFn(req)
	}
	return platforms.ErrUnsupported
}

func (f *fakePlatform) Compact(context.Context, platforms.CompactRequest) error {
	return platforms.ErrUnsupported
}

func (f *fakePlatform) CreateSession(_ context.Context, req platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
	if f.createSessionFn != nil {
		return f.createSessionFn(req)
	}
	return nil, platforms.ErrUnsupported
}

func (f *fakePlatform) ProxyEvents(ctx context.Context, sessionID string, w io.Writer, flush func()) error {
	if f.proxyEventsFn != nil {
		return f.proxyEventsFn(ctx, sessionID, w, flush)
	}
	return platforms.ErrUnsupported
}
