package claudecode

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/claudeprovider"
	"github.com/NoUseFreak/ocman/internal/hostsvc/local"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/provider"
	"github.com/NoUseFreak/ocman/internal/providerprotocol"
)

// drained is an out-of-range supervisor being drained (AD-5): an adapter
// limited to its version-independent routes, whose prompt events feed the
// dashboard pipeline (rules, judge, inbox) like an attached supervisor's
// and are fanned out to the browser streams of its sessions.
type drained struct {
	adapter *provider.Adapter
	stop    context.CancelFunc // nil until its event stream runs
	done    chan struct{}      // closed when the drain ends
	mu      sync.Mutex
	subs    map[chan providerprotocol.Event]string // browser streams, by session
}

func (d *drained) start(ctx context.Context) {
	if d.stop != nil {
		return
	}
	ctx, d.stop = context.WithCancel(ctx)
	go func() { _ = d.adapter.WatchEvents(ctx) }()
}

func (d *drained) close() {
	if d.stop != nil {
		d.stop()
	}
	d.adapter.Close()
	close(d.done)
}

func (d *drained) broadcast(e providerprotocol.Event) {
	var props struct {
		SessionID string `json:"sessionID"`
	}
	_ = json.Unmarshal(e.Properties, &props)
	d.mu.Lock()
	defer d.mu.Unlock()
	for ch, id := range d.subs {
		if id == props.SessionID {
			select {
			case ch <- e:
			default: // ponytail: a stalled browser misses it; its reconnect lists pending prompts
			}
		}
	}
}

func (d *drained) subscribe(id string) (chan providerprotocol.Event, func()) {
	ch := make(chan providerprotocol.Event, 64)
	d.mu.Lock()
	d.subs[ch] = id
	d.mu.Unlock()
	return ch, func() { d.mu.Lock(); delete(d.subs, ch); d.mu.Unlock() }
}

// onDrain keeps root's draining supervisor answerable until it settles
// (AD-5): a compatible one (an earlier launch configuration) stays attached
// with its full event stream; an out-of-range one is reached through
// provider.NewDraining, whose prompts are pushed, never polled.
func (c *claudeProjects) onDrain(root, endpoint string, compatible bool) {
	claudeTestHook("drain", root)
	var d *drained
	if compatible {
		if _, err := c.attach(c.ctx, root, endpoint); err != nil {
			log.WithError(err).WithField("root", root).Warn("attaching a draining Claude supervisor")
		}
	} else if a, err := provider.NewDraining(c.ctx, strings.TrimPrefix(endpoint, "unix://")); err != nil {
		log.WithError(err).WithField("root", root).Warn("connecting to a draining Claude supervisor")
	} else {
		d = &drained{adapter: a, done: make(chan struct{}), subs: map[chan providerprotocol.Event]string{}}
		a.SetRules(c.rules) // Allow-always persists and inherits as for an attached supervisor
		a.SetEventHandler(func(e providerprotocol.Event) {
			c.mu.Lock()
			handler := c.handler
			c.mu.Unlock()
			if handler != nil {
				handler(e)
			}
			d.broadcast(e)
		})
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.endDrainLocked(root)
	c.draining[root] = d
	if d != nil {
		d.adapter.SetSessionEventSink(c.sink)
		if c.watch != nil {
			d.start(c.watch)
		}
		// A browser that connected before recovery found this supervisor
		// switches to its prompts (ProxyEvents).
		close(c.attached)
		c.attached = make(chan struct{})
	}
}

// claudeTestHook lets tests order recovery against a browser stream.
var claudeTestHook = func(point, key string) {}

func (c *claudeProjects) endDrainLocked(root string) {
	if d := c.draining[root]; d != nil {
		d.close()
	}
	delete(c.draining, root)
}

// drainingFor is the drained supervisor of a launched session whose
// out-of-range supervisor is draining, or nil.
func (c *claudeProjects) drainingFor(ctx context.Context, id string) *drained {
	dir, owned := claudeprovider.OwnedDirectory(c.statePath, id)
	if !owned {
		return nil
	}
	root, err := local.ProviderRoot(ctx, dir)
	if err != nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.draining[root]
}

// promptAdapter serves prompt answers and abort, which a draining
// supervisor still accepts.
func (p *claudePlatform) promptAdapter(ctx context.Context, id string) (*provider.Adapter, error) {
	if d := p.projects.drainingFor(ctx, id); d != nil {
		return d.adapter, nil
	}
	return p.adapter(ctx, id, false)
}

// proxyDraining is a draining session's browser stream: the transcript from
// the browsing supervisor, plus its pending prompts and every later prompt
// event from the draining supervisor. It ends with the drain, so the client
// reconnects to the replacement.
func (p *claudePlatform) proxyDraining(ctx context.Context, id string, d *drained, w io.Writer, flush func()) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var mu sync.Mutex
	out := lockedFlush{mu: &mu, w: w, flush: flush}
	events, unsubscribe := d.subscribe(id) // before listing: nothing falls between
	defer unsubscribe()
	browsing := make(chan error, 1)
	go func() { browsing <- p.Adapter.ProxyEvents(ctx, id, out, out.Flush) }()
	for typ, list := range map[string]func(context.Context, string) ([]platforms.LivePrompt, error){
		"permission.asked": d.adapter.ListPermissions, "question.asked": d.adapter.ListQuestions} {
		prompts, _ := list(ctx, id)
		for _, pr := range prompts {
			props, _ := json.Marshal(pr)
			if err := out.event(providerprotocol.Event{Type: typ, Properties: props}); err != nil {
				return err
			}
		}
	}
	for {
		select {
		case e := <-events:
			if err := out.event(e); err != nil {
				return err
			}
		case err := <-browsing:
			return err
		case <-d.done:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// lockedFlush serializes two streams writing one SSE response.
type lockedFlush struct {
	mu    *sync.Mutex
	w     io.Writer
	flush func()
}

func (l lockedFlush) Write(b []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(b)
}

func (l lockedFlush) Flush() {
	if l.flush != nil {
		l.mu.Lock()
		l.flush()
		l.mu.Unlock()
	}
}

// event writes one unsequenced SSE frame: the draining supervisor's
// sequence is not the browsing stream's.
func (l lockedFlush) event(e providerprotocol.Event) error {
	payload, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(l, "data: %s\n\n", payload); err != nil {
		return err
	}
	l.Flush()
	return nil
}
