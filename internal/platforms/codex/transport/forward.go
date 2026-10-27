package codextransport

import (
	"context"
	"io"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	codexprovider "github.com/NoUseFreak/ocman/internal/platforms/codex/provider"
	"github.com/NoUseFreak/ocman/internal/platforms/provider"
	"github.com/NoUseFreak/ocman/internal/providerprotocol"
)

// Session operations go to the supervisor, launching it when needed. Reads of
// in-memory state never launch one: before it runs there is nothing live.

func (p *Platform) LiveStatus(id string) *platforms.LiveState {
	if a := p.current(); a != nil {
		return a.LiveStatus(id)
	}
	return nil
}

func (p *Platform) ListPermissions(ctx context.Context, id string) ([]platforms.LivePrompt, error) {
	if a := p.current(); a != nil {
		return a.ListPermissions(ctx, id)
	}
	return nil, nil
}

func (p *Platform) ListQuestions(ctx context.Context, id string) ([]platforms.LivePrompt, error) {
	if a := p.current(); a != nil {
		return a.ListQuestions(ctx, id)
	}
	return nil, nil
}

// RefreshPermissions reconciles with the supervisor; with none running no
// prompt can be pending.
func (p *Platform) RefreshPermissions(ctx context.Context, id string) ([]platforms.LivePrompt, error) {
	if a := p.current(); a != nil {
		return a.RefreshPermissions(ctx, id)
	}
	return nil, nil
}

func (p *Platform) ReplyOutcomes(ctx context.Context, id string) bool {
	if a := p.current(); a != nil {
		return a.ReplyOutcomes(ctx, id)
	}
	return true
}

// Sessions is the runtime inventory, read from Codex's own thread store so
// identity migration needs no supervisor.
func (p *Platform) Sessions(ctx context.Context, dir string, since int64) ([]db.Session, error) {
	return p.history.sessions(ctx, dir, since)
}

func (p *Platform) SessionLifecycle(ctx context.Context, id string) (*platforms.SessionLifecycle, error) {
	var out *platforms.SessionLifecycle
	err := p.do(ctx, func(a *provider.Adapter) (err error) { out, err = a.SessionLifecycle(ctx, id); return err })
	return out, err
}

func (p *Platform) CreateSession(ctx context.Context, req platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
	var out *platforms.CreateSessionResponse
	err := p.do(ctx, func(a *provider.Adapter) (err error) { out, err = a.CreateSession(ctx, req); return err })
	return out, err
}

func (p *Platform) ForkSession(ctx context.Context, req platforms.ForkSessionRequest) (*platforms.CreateSessionResponse, error) {
	var out *platforms.CreateSessionResponse
	err := p.do(ctx, func(a *provider.Adapter) (err error) { out, err = a.ForkSession(ctx, req); return err })
	return out, err
}

func (p *Platform) SendMessage(ctx context.Context, req platforms.SendMessageRequest) error {
	return p.doSession(ctx, req.SessionID, func(a *provider.Adapter) error { return a.SendMessage(ctx, req) })
}

func (p *Platform) ExecuteCommand(ctx context.Context, req platforms.ExecuteCommandRequest) error {
	return p.doSession(ctx, req.SessionID, func(a *provider.Adapter) error { return a.ExecuteCommand(ctx, req) })
}

func (p *Platform) RunShell(ctx context.Context, req platforms.RunShellRequest) error {
	return p.doSession(ctx, req.SessionID, func(a *provider.Adapter) error { return a.RunShell(ctx, req) })
}

func (p *Platform) RespondPermission(ctx context.Context, req platforms.RespondPermissionRequest) error {
	return p.reply(ctx, func(a *provider.Adapter) error { return a.RespondPermission(ctx, req) })
}

func (p *Platform) RespondQuestion(ctx context.Context, req platforms.RespondQuestionRequest) error {
	return p.reply(ctx, func(a *provider.Adapter) error { return a.RespondQuestion(ctx, req) })
}

func (p *Platform) RejectQuestion(ctx context.Context, req platforms.RejectQuestionRequest) error {
	return p.reply(ctx, func(a *provider.Adapter) error { return a.RejectQuestion(ctx, req) })
}

// Abort never launches a supervisor: without one nothing is running.
func (p *Platform) Abort(ctx context.Context, req platforms.AbortRequest) error {
	if a := p.current(); a != nil {
		return a.Abort(ctx, req)
	}
	return nil
}

func (p *Platform) RenameSession(ctx context.Context, req platforms.RenameSessionRequest) error {
	return p.doSession(ctx, req.SessionID, func(a *provider.Adapter) error { return a.RenameSession(ctx, req) })
}

// Rulesets are ocman's own (provider.Rules); the adapter serves them
// locally, so this reaches the supervisor only to attach to it.
func (p *Platform) PermissionRules(ctx context.Context, id string) ([]platforms.PermissionRule, error) {
	var out []platforms.PermissionRule
	err := p.do(ctx, func(a *provider.Adapter) (err error) { out, err = a.PermissionRules(ctx, id); return err })
	return out, err
}

func (p *Platform) SetPermissionRules(ctx context.Context, req platforms.SetPermissionRulesRequest) error {
	return p.do(ctx, func(a *provider.Adapter) error { return a.SetPermissionRules(ctx, req) })
}

func (p *Platform) Compact(ctx context.Context, req platforms.CompactRequest) error {
	return p.doSession(ctx, req.SessionID, func(a *provider.Adapter) error { return a.Compact(ctx, req) })
}

// ChangesFromStored and InfoFromStored project the session panels from
// stored history (platforms.StoredDetailProjector).
func (p *Platform) ChangesFromStored(ctx context.Context, detail *platforms.SessionDetail) (*platforms.SessionChanges, error) {
	return codexprovider.ChangesFromStored(ctx, detail)
}

func (p *Platform) InfoFromStored(ctx context.Context, detail *platforms.SessionDetail) (*platforms.SessionInfo, error) {
	return codexprovider.InfoFromStored(ctx, detail)
}

func (p *Platform) MoveSession(context.Context, platforms.MoveSessionRequest) error {
	return platforms.ErrUnsupported
}

func (p *Platform) ProxyEvents(ctx context.Context, id string, w io.Writer, flush func()) error {
	return p.do(ctx, func(a *provider.Adapter) error { return a.ProxyEvents(ctx, id, w, flush) })
}

// SetEventHandler and SetSessionEventSink apply to the current adapter and
// every later one.
func (p *Platform) SetEventHandler(handler func(providerprotocol.Event)) {
	p.mu.Lock()
	p.handler = handler
	a := p.adapter
	p.mu.Unlock()
	if a != nil {
		a.SetEventHandler(handler)
	}
}

func (p *Platform) SetSessionEventSink(sink platforms.SessionEventSink) {
	p.mu.Lock()
	p.sink = sink
	a := p.adapter
	p.mu.Unlock()
	if a != nil {
		a.SetSessionEventSink(sink)
	}
}

// WatchEvents runs the headless stream of the current supervisor and of any
// that replaces it, until ctx ends.
func (p *Platform) WatchEvents(ctx context.Context) error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return context.Canceled
	}
	p.watch = ctx
	p.startWatchLocked()
	p.mu.Unlock()
	<-ctx.Done()
	return ctx.Err()
}
