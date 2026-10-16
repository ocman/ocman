package claudecode

import (
	"context"
	"io"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/provider"
	"github.com/NoUseFreak/ocman/internal/providerprotocol"
)

// ProxyEvents streams from the session's project supervisor. A launched
// session whose supervisor is not attached yet gets the browsing stream
// until recovery finds it: attached, the stream ends so the client
// reconnects to it; draining (out of range), the same stream continues
// with the draining supervisor's prompts.
func (p *claudePlatform) ProxyEvents(ctx context.Context, id string, w io.Writer, flush func()) error {
	// The notification is captured before the state is read: recovery
	// installs a supervisor and closes this channel under one lock, so one
	// found after this point closes it, and one found before is seen below.
	p.projects.mu.Lock()
	attached := p.projects.attached
	p.projects.mu.Unlock()
	if d := p.projects.drainingFor(ctx, id); d != nil {
		return p.proxyDraining(ctx, id, d, w, flush)
	}
	a, _, err := p.projects.route(ctx, id, false)
	if err != nil {
		return err
	}
	claudeTestHook("routed", id)
	if a != nil {
		return a.ProxyEvents(ctx, id, w, flush)
	}
	browse, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		select {
		case <-attached:
			cancel()
		case <-browse.Done():
		}
	}()
	claudeTestHook("browsing", id)
	err = p.Adapter.ProxyEvents(browse, id, w, flush)
	select {
	case <-attached:
		if d := p.projects.drainingFor(ctx, id); d != nil {
			return p.proxyDraining(ctx, id, d, w, flush)
		}
		return nil
	default:
		return err
	}
}

// WatchEvents runs the headless stream of the browsing supervisor and of
// every project supervisor, including ones attached later.
func (p *claudePlatform) WatchEvents(ctx context.Context) error {
	c := p.projects
	c.mu.Lock()
	c.watch = ctx
	if c.closed {
		c.mu.Unlock()
		return context.Canceled
	}
	for _, pr := range c.roots {
		pr.start(ctx, &c.wg)
	}
	for _, d := range c.draining {
		if d != nil {
			d.start(ctx, &c.wg)
		}
	}
	c.mu.Unlock()
	return p.Adapter.WatchEvents(ctx)
}

func (p *claudePlatform) SetEventHandler(handler func(providerprotocol.Event)) {
	p.Adapter.SetEventHandler(handler)
	p.projects.mu.Lock()
	p.projects.handler = handler
	p.projects.mu.Unlock()
	p.projects.each(func(a *provider.Adapter) { a.SetEventHandler(handler) })
}

func (p *claudePlatform) SetSessionEventSink(sink platforms.SessionEventSink) {
	p.Adapter.SetSessionEventSink(sink)
	p.projects.mu.Lock()
	p.projects.sink = sink
	for _, d := range p.projects.draining {
		if d != nil {
			d.adapter.SetSessionEventSink(sink)
		}
	}
	p.projects.mu.Unlock()
	p.projects.each(func(a *provider.Adapter) { a.SetSessionEventSink(sink) })
}
