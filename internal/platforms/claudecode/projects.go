package claudecode

import (
	"context"
	"fmt"
	"strings"
	"sync"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/claudeprovider"
	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/hostsvc/local"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/provider"
	"github.com/NoUseFreak/ocman/internal/providerprotocol"
)

// claudeProjects drives the sessions ocman launched through one tmux-hosted
// supervisor per repository root (AD-3), each reached by its own provider
// adapter. Those supervisors outlive ocman: a restart re-probes the
// persisted endpoints and replays their spools (AD-4). Browsing every
// transcript stays with the in-process supervisor and launches nothing.
type claudeProjects struct {
	ctx       context.Context // the platform's lifetime
	stop      context.CancelFunc
	wg        sync.WaitGroup // recovery and event streams, joined by close
	mgr       *local.ProviderManager
	rules     *provider.Rules
	statePath string // claude-sessions.json: which sessions ocman launched

	mu       sync.Mutex
	roots    map[string]*claudeProject
	sink     platforms.SessionEventSink
	handler  func(providerprotocol.Event)
	watch    context.Context // set once WatchEvents runs
	attached chan struct{}   // closed and replaced on every attach
	// draining holds roots whose supervisor is being drained for
	// replacement: an out-of-range one reached through its version-
	// independent routes, nil when the attached adapter still serves it.
	draining map[string]*drained
	closed   bool
}

type claudeProject struct {
	watched
	endpoint string
}

// watched is an adapter whose headless event stream may run.
type watched struct {
	adapter  *provider.Adapter
	stop     context.CancelFunc // nil until its event stream runs
	watching chan struct{}      // closed when that stream returns
}

func (w *watched) start(ctx context.Context, wg *sync.WaitGroup) {
	if w.stop != nil {
		return
	}
	ctx, w.stop = context.WithCancel(ctx)
	w.watching = make(chan struct{})
	wg.Go(func() {
		defer close(w.watching)
		_ = w.adapter.WatchEvents(ctx)
	})
}

// retire stops the stream and closes the adapter once it has returned,
// without waiting: callers hold c.mu, which the stream's handler takes.
func (w *watched) retire(wg *sync.WaitGroup) {
	if w.stop == nil {
		w.adapter.Close()
		return
	}
	w.stop()
	wg.Go(func() {
		<-w.watching
		w.adapter.Close()
	})
}

// close stops recovery and every event stream and waits for them, so none
// outlives the composition (or the state.db it writes through).
func (c *claudeProjects) close() {
	if c.stop != nil {
		c.stop()
	}
	c.mu.Lock()
	c.closed = true
	for root, p := range c.roots {
		p.retire(&c.wg)
		delete(c.roots, root)
	}
	for root := range c.draining {
		c.endDrainLocked(root)
	}
	c.mu.Unlock()
	c.wg.Wait()
}

// recover reattaches every persisted supervisor that is still running.
func (c *claudeProjects) recover(ctx context.Context) {
	roots, err := c.mgr.State.ListManagedProviders(ctx, c.mgr.Runtime.PlatformID)
	if err != nil {
		log.WithError(err).Warn("listing Claude project supervisors")
		return
	}
	for _, root := range roots {
		c.wg.Go(func() {
			res, ok, err := c.mgr.Recover(ctx, root)
			if err == nil && ok {
				_, err = c.attach(ctx, res.RepoRoot, res.Endpoint)
			}
			if err != nil {
				c.mu.Lock()
				c.endDrainLocked(root) // sends may launch again
				c.mu.Unlock()
				log.WithError(err).WithField("root", root).Warn("recovering Claude project supervisor")
			}
		})
	}
}

// attach binds root's supervisor at endpoint, replacing an adapter for an
// earlier endpoint: a relaunched supervisor starts a new sequence.
func (c *claudeProjects) attach(ctx context.Context, root, endpoint string) (*provider.Adapter, error) {
	c.mu.Lock()
	if p := c.roots[root]; p != nil && p.endpoint == endpoint {
		c.mu.Unlock()
		return p.adapter, nil
	}
	c.mu.Unlock()
	a, err := provider.New(ctx, strings.TrimPrefix(endpoint, "unix://"))
	if err != nil {
		return nil, err
	}
	a.SetRules(c.rules)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		a.Close()
		return nil, fmt.Errorf("claude platform closed")
	}
	if p := c.roots[root]; p != nil {
		if p.endpoint == endpoint { // a concurrent attach won
			a.Close()
			return p.adapter, nil
		}
		p.retire(&c.wg)
	}
	a.SetSessionEventSink(c.sink)
	a.SetEventHandler(c.handler)
	p := &claudeProject{watched: watched{adapter: a}, endpoint: endpoint}
	if c.watch != nil {
		p.start(c.watch, &c.wg)
	}
	c.roots[root] = p
	c.endDrainLocked(root)
	close(c.attached)
	c.attached = make(chan struct{})
	return a, nil
}

// ensure launches or re-probes dir's project supervisor.
func (c *claudeProjects) ensure(ctx context.Context, dir string) (*hostsvc.EnsureRuntimeResult, *provider.Adapter, error) {
	if root, err := local.ProviderRoot(ctx, dir); err == nil {
		c.mu.Lock()
		_, draining := c.draining[root]
		c.mu.Unlock()
		if draining {
			return nil, nil, fmt.Errorf("%w: the Claude supervisor for %s is being replaced; retry once its sessions settle", platforms.ErrBusy, root)
		}
	}
	res, err := c.mgr.Ensure(ctx, dir)
	if err != nil {
		return nil, nil, err
	}
	a, err := c.attach(ctx, res.RepoRoot, res.Endpoint)
	return res, a, err
}

// owner is the attached adapter that has seen session id.
func (c *claudeProjects) owner(id string) *provider.Adapter {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, p := range c.roots {
		if p.adapter.Owns(context.Background(), id) {
			return p.adapter
		}
	}
	return nil
}

// adapters snapshots the attached adapters, for calls made without c.mu.
func (c *claudeProjects) adapters() []*provider.Adapter {
	var out []*provider.Adapter
	c.each(func(a *provider.Adapter) { out = append(out, a) })
	return out
}

func (c *claudeProjects) each(fn func(*provider.Adapter)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, p := range c.roots {
		fn(p.adapter)
	}
}

// route picks the adapter for session id. A launched session goes to its
// project supervisor, started when launch is set; any other session is a
// terminal session served read-only by the browsing supervisor (nil).
func (c *claudeProjects) route(ctx context.Context, id string, launch bool) (*provider.Adapter, bool, error) {
	dir, owned := claudeprovider.OwnedDirectory(c.statePath, id)
	if owned && launch {
		_, a, err := c.ensure(ctx, dir)
		return a, true, err
	}
	if a := c.owner(id); a != nil {
		return a, owned, nil
	}
	if owned {
		if root, err := local.ProviderRoot(ctx, dir); err == nil {
			c.mu.Lock()
			defer c.mu.Unlock()
			if p := c.roots[root]; p != nil {
				return p.adapter, true, nil
			}
		}
	}
	return nil, owned, nil
}

// claudePlatform methods below route session work to project supervisors;
// everything else is the embedded browsing adapter.

func (p *claudePlatform) adapter(ctx context.Context, id string, launch bool) (*provider.Adapter, error) {
	a, _, err := p.projects.route(ctx, id, launch)
	if a == nil && err == nil {
		a = p.Adapter
	}
	return a, err
}

func (p *claudePlatform) EnsureRuntime(ctx context.Context, dir string) (*hostsvc.EnsureRuntimeResult, error) {
	res, _, err := p.projects.ensure(ctx, dir)
	return res, err
}

func (p *claudePlatform) CreateSession(ctx context.Context, req platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
	_, a, err := p.projects.ensure(ctx, req.Directory)
	if err != nil {
		return nil, err
	}
	return a.CreateSession(ctx, req)
}

func (p *claudePlatform) Owns(ctx context.Context, id string) bool {
	return p.Adapter.Owns(ctx, id) || p.projects.owner(id) != nil
}

// Sessions lists every transcript and overlays what the project
// supervisors settled: status (failed and aborted included) and a limited
// session's reset time, which the transcript cannot tell (FR-7, AD-10).
func (p *claudePlatform) Sessions(ctx context.Context, dir string, since int64) ([]db.Session, error) {
	sessions, err := p.Adapter.Sessions(ctx, dir, since)
	settled := map[string]db.Session{}
	for _, a := range p.projects.adapters() {
		live, lerr := a.LiveSessions(ctx)
		if lerr != nil {
			log.WithError(lerr).Debug("reading Claude supervisor statuses")
		}
		for _, s := range live {
			settled[s.ID] = s
		}
	}
	for i := range sessions {
		s := &sessions[i]
		if st, ok := settled[s.ID]; ok {
			s.Status, s.ResetsAt = st.Status, st.ResetsAt
		}
		// A prompt waiting for the user is only in the adapter's view.
		if a := p.projects.owner(s.ID); a != nil {
			if live := a.LiveStatus(s.ID); live != nil {
				s.PendingPermission, s.PendingQuestion = live.PendingPermission, live.PendingQuestion
				if s.Status == db.StatusBusy && (live.Status == string(db.StatusWaiting) || s.PendingPermission || s.PendingQuestion) {
					s.Status = db.StatusWaiting
				}
			}
		}
	}
	return sessions, err
}

func (p *claudePlatform) Session(ctx context.Context, id string, limit, offset int) (*platforms.SessionDetail, error) {
	a, err := p.adapter(ctx, id, false)
	if err != nil {
		return nil, err
	}
	return a.Session(ctx, id, limit, offset)
}

func (p *claudePlatform) SessionLifecycle(ctx context.Context, id string) (*platforms.SessionLifecycle, error) {
	a, err := p.adapter(ctx, id, false)
	if err != nil {
		return nil, err
	}
	return a.SessionLifecycle(ctx, id)
}

func (p *claudePlatform) LiveStatus(id string) *platforms.LiveState {
	if a := p.projects.owner(id); a != nil {
		return a.LiveStatus(id)
	}
	return p.Adapter.LiveStatus(id)
}

func (p *claudePlatform) ListPermissions(ctx context.Context, id string) ([]platforms.LivePrompt, error) {
	a, err := p.promptAdapter(ctx, id)
	if err != nil {
		return nil, err
	}
	return a.ListPermissions(ctx, id)
}

func (p *claudePlatform) ListQuestions(ctx context.Context, id string) ([]platforms.LivePrompt, error) {
	a, err := p.promptAdapter(ctx, id)
	if err != nil {
		return nil, err
	}
	return a.ListQuestions(ctx, id)
}

func (p *claudePlatform) RefreshPermissions(ctx context.Context, id string) ([]platforms.LivePrompt, error) {
	a, err := p.promptAdapter(ctx, id)
	if err != nil {
		return nil, err
	}
	return a.RefreshPermissions(ctx, id)
}

func (p *claudePlatform) RefreshQuestions(ctx context.Context, id string) ([]platforms.LivePrompt, error) {
	a, err := p.promptAdapter(ctx, id)
	if err != nil {
		return nil, err
	}
	return a.RefreshQuestions(ctx, id)
}

func (p *claudePlatform) SendMessage(ctx context.Context, req platforms.SendMessageRequest) error {
	a, err := p.adapter(ctx, req.SessionID, true)
	if err != nil {
		return err
	}
	return a.SendMessage(ctx, req)
}

func (p *claudePlatform) ExecuteCommand(ctx context.Context, req platforms.ExecuteCommandRequest) error {
	a, err := p.adapter(ctx, req.SessionID, true)
	if err != nil {
		return err
	}
	return a.ExecuteCommand(ctx, req)
}

func (p *claudePlatform) Compact(ctx context.Context, req platforms.CompactRequest) error {
	a, err := p.adapter(ctx, req.SessionID, true)
	if err != nil {
		return err
	}
	return a.Compact(ctx, req)
}

func (p *claudePlatform) RenameSession(ctx context.Context, req platforms.RenameSessionRequest) error {
	a, err := p.adapter(ctx, req.SessionID, true)
	if err != nil {
		return err
	}
	return a.RenameSession(ctx, req)
}

func (p *claudePlatform) ForkSession(ctx context.Context, req platforms.ForkSessionRequest) (*platforms.CreateSessionResponse, error) {
	a, err := p.adapter(ctx, req.SessionID, true)
	if err != nil {
		return nil, err
	}
	return a.ForkSession(ctx, req)
}

func (p *claudePlatform) Abort(ctx context.Context, req platforms.AbortRequest) error {
	a, err := p.promptAdapter(ctx, req.SessionID)
	if err != nil {
		return err
	}
	return a.Abort(ctx, req)
}

func (p *claudePlatform) RespondPermission(ctx context.Context, req platforms.RespondPermissionRequest) error {
	a, err := p.promptAdapter(ctx, req.SessionID)
	if err != nil {
		return err
	}
	return a.RespondPermission(ctx, req)
}

// ReplyOutcomes reports for the supervisor that answers id's prompts.
func (p *claudePlatform) ReplyOutcomes(ctx context.Context, id string) bool {
	a, err := p.promptAdapter(ctx, id)
	return err == nil && a.ReplyOutcomes(ctx, id)
}

func (p *claudePlatform) RespondQuestion(ctx context.Context, req platforms.RespondQuestionRequest) error {
	a, err := p.promptAdapter(ctx, req.SessionID)
	if err != nil {
		return err
	}
	return a.RespondQuestion(ctx, req)
}

func (p *claudePlatform) RejectQuestion(ctx context.Context, req platforms.RejectQuestionRequest) error {
	a, err := p.promptAdapter(ctx, req.SessionID)
	if err != nil {
		return err
	}
	return a.RejectQuestion(ctx, req)
}
