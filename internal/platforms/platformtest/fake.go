// Package platformtest provides a configurable platforms.Platform for tests.
// Import it only from tests.
package platformtest

import (
	"context"
	"io"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
)

// Fake is a configurable platforms.Platform. The zero value is usable: its
// ID is "fake", it is available, reads return empty results and every
// operation returns platforms.ErrUnsupported. Tests set only the fields
// they need; a non-nil Fn field replaces the matching method.
type Fake struct {
	PlatformID   string
	Name         string
	Availability *bool // nil reports available
	Caps         platforms.Capabilities

	SessionList []db.Session
	SessionsErr error
	SessionsFn  func(ctx context.Context, dir string, since int64) ([]db.Session, error)
	SessionFn   func(id string) (*platforms.SessionDetail, error)
	Inactive    []db.SessionArchiveCandidate
	InactiveErr error
	Live        *platforms.LiveState
	Changes     *platforms.SessionChanges
	ChangesErr  error
	Info        *platforms.SessionInfo
	InfoErr     error

	SessionModelsFn    func() *platforms.SessionModelsResponse
	DirectoryCatalogFn func(req platforms.DirectoryCatalogRequest) (*platforms.DirectoryCatalog, error)
	ListPermissionsFn  func(sessionID string) ([]platforms.LivePrompt, error)
	ListQuestionsFn    func(sessionID string) ([]platforms.LivePrompt, error)
	PermissionRulesFn  func(sessionID string) ([]platforms.PermissionRule, error)

	SendMessageFn        func(req platforms.SendMessageRequest) error
	ExecuteCommandFn     func(req platforms.ExecuteCommandRequest) error
	RunShellFn           func() error
	RespondPermissionFn  func(req platforms.RespondPermissionRequest) error
	RespondQuestionFn    func(req platforms.RespondQuestionRequest) error
	RejectQuestionFn     func(req platforms.RejectQuestionRequest) error
	AbortFn              func(req platforms.AbortRequest) error
	RevertFn             func(req platforms.RevertSessionRequest) error
	UnrevertFn           func(req platforms.UnrevertSessionRequest) error
	RenameSessionFn      func(req platforms.RenameSessionRequest) error
	SetPermissionRulesFn func(req platforms.SetPermissionRulesRequest) error
	CompactFn            func(req platforms.CompactRequest) error
	ForkSessionFn        func(req platforms.ForkSessionRequest) (*platforms.CreateSessionResponse, error)
	MoveSessionFn        func(req platforms.MoveSessionRequest) error
	CreateSessionFn      func(req platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error)
	DisposeFn            func(req platforms.DisposeSessionRequest) error
	ProxyEventsFn        func(ctx context.Context, sessionID string, w io.Writer, flush func()) error
}

var _ platforms.Platform = (*Fake)(nil)

func (f *Fake) ID() platforms.ID {
	if f.PlatformID == "" {
		return "fake"
	}
	return platforms.ID(f.PlatformID)
}

func (f *Fake) DisplayName() string {
	if f.Name == "" {
		return string(f.ID())
	}
	return f.Name
}

func (f *Fake) Available(context.Context) bool {
	return f.Availability == nil || *f.Availability
}

func (f *Fake) Capabilities() platforms.Capabilities { return f.Caps }

func (f *Fake) Sessions(ctx context.Context, dir string, since int64) ([]db.Session, error) {
	if f.SessionsFn != nil {
		return f.SessionsFn(ctx, dir, since)
	}
	return f.SessionList, f.SessionsErr
}

func (f *Fake) Session(_ context.Context, id string, _, _ int) (*platforms.SessionDetail, error) {
	if f.SessionFn != nil {
		return f.SessionFn(id)
	}
	return nil, platforms.ErrNotFound
}

// Owns reports whether SessionList contains sessionID.
func (f *Fake) Owns(_ context.Context, sessionID string) bool {
	for _, s := range f.SessionList {
		if s.ID == sessionID {
			return true
		}
	}
	return false
}

func (f *Fake) SessionsInactiveBefore(context.Context, int64) ([]db.SessionArchiveCandidate, error) {
	return f.Inactive, f.InactiveErr
}

func (f *Fake) SessionChanges(context.Context, string) (*platforms.SessionChanges, error) {
	if f.Changes != nil {
		return f.Changes, f.ChangesErr
	}
	return nil, platforms.ErrUnsupported
}

func (f *Fake) SessionInfo(context.Context, string) (*platforms.SessionInfo, error) {
	if f.Info != nil || f.InfoErr != nil {
		return f.Info, f.InfoErr
	}
	return nil, platforms.ErrUnsupported
}

func (f *Fake) LiveStatus(string) *platforms.LiveState { return f.Live }

func (f *Fake) AgentCatalog(context.Context, string) ([]platforms.AgentCatalogEntry, error) {
	return nil, nil
}

func (f *Fake) SlashCommands(context.Context, string) ([]platforms.SlashCommandEntry, error) {
	return nil, nil
}

func (f *Fake) SessionModels(context.Context, string) (*platforms.SessionModelsResponse, error) {
	if f.SessionModelsFn != nil {
		return f.SessionModelsFn(), nil
	}
	return nil, nil
}

func (f *Fake) DirectoryCatalog(_ context.Context, req platforms.DirectoryCatalogRequest) (*platforms.DirectoryCatalog, error) {
	if f.DirectoryCatalogFn != nil {
		return f.DirectoryCatalogFn(req)
	}
	return &platforms.DirectoryCatalog{
		Agents:   []platforms.AgentCatalogEntry{},
		Commands: []platforms.SlashCommandEntry{},
		Models:   &platforms.SessionModelsResponse{Models: []platforms.SessionModel{}},
	}, nil
}

func (f *Fake) ListPermissions(_ context.Context, sessionID string) ([]platforms.LivePrompt, error) {
	if f.ListPermissionsFn != nil {
		return f.ListPermissionsFn(sessionID)
	}
	return nil, nil
}

func (f *Fake) ListQuestions(_ context.Context, sessionID string) ([]platforms.LivePrompt, error) {
	if f.ListQuestionsFn != nil {
		return f.ListQuestionsFn(sessionID)
	}
	return nil, nil
}

func (f *Fake) PermissionRules(_ context.Context, sessionID string) ([]platforms.PermissionRule, error) {
	if f.PermissionRulesFn != nil {
		return f.PermissionRulesFn(sessionID)
	}
	return nil, platforms.ErrUnsupported
}

func (f *Fake) SendMessage(_ context.Context, req platforms.SendMessageRequest) error {
	return call(f.SendMessageFn, req)
}

func (f *Fake) ExecuteCommand(_ context.Context, req platforms.ExecuteCommandRequest) error {
	return call(f.ExecuteCommandFn, req)
}

func (f *Fake) RunShell(context.Context, platforms.RunShellRequest) error {
	if f.RunShellFn != nil {
		return f.RunShellFn()
	}
	return platforms.ErrUnsupported
}

func (f *Fake) RespondPermission(_ context.Context, req platforms.RespondPermissionRequest) error {
	return call(f.RespondPermissionFn, req)
}

func (f *Fake) RespondQuestion(_ context.Context, req platforms.RespondQuestionRequest) error {
	return call(f.RespondQuestionFn, req)
}

func (f *Fake) RejectQuestion(_ context.Context, req platforms.RejectQuestionRequest) error {
	return call(f.RejectQuestionFn, req)
}

func (f *Fake) Abort(_ context.Context, req platforms.AbortRequest) error {
	return call(f.AbortFn, req)
}

func (f *Fake) Revert(_ context.Context, req platforms.RevertSessionRequest) error {
	return call(f.RevertFn, req)
}

func (f *Fake) Unrevert(_ context.Context, req platforms.UnrevertSessionRequest) error {
	return call(f.UnrevertFn, req)
}

func (f *Fake) RenameSession(_ context.Context, req platforms.RenameSessionRequest) error {
	return call(f.RenameSessionFn, req)
}

func (f *Fake) SetPermissionRules(_ context.Context, req platforms.SetPermissionRulesRequest) error {
	return call(f.SetPermissionRulesFn, req)
}

func (f *Fake) Compact(_ context.Context, req platforms.CompactRequest) error {
	return call(f.CompactFn, req)
}

func (f *Fake) MoveSession(_ context.Context, req platforms.MoveSessionRequest) error {
	return call(f.MoveSessionFn, req)
}

func (f *Fake) DisposeSession(_ context.Context, req platforms.DisposeSessionRequest) error {
	if f.DisposeFn != nil {
		return f.DisposeFn(req)
	}
	return nil
}

func (f *Fake) ForkSession(_ context.Context, req platforms.ForkSessionRequest) (*platforms.CreateSessionResponse, error) {
	if f.ForkSessionFn != nil {
		return f.ForkSessionFn(req)
	}
	return nil, platforms.ErrUnsupported
}

func (f *Fake) CreateSession(_ context.Context, req platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
	if f.CreateSessionFn != nil {
		return f.CreateSessionFn(req)
	}
	return nil, platforms.ErrUnsupported
}

func (f *Fake) ProxyEvents(ctx context.Context, sessionID string, w io.Writer, flush func()) error {
	if f.ProxyEventsFn != nil {
		return f.ProxyEventsFn(ctx, sessionID, w, flush)
	}
	return platforms.ErrUnsupported
}

func call[R any](fn func(R) error, req R) error {
	if fn != nil {
		return fn(req)
	}
	return platforms.ErrUnsupported
}
