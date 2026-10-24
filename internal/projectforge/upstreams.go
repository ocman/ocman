package projectforge

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/forge"
	"github.com/NoUseFreak/ocman/internal/git"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/httpx"
)

const (
	upstreamsTTL        = 10 * time.Second
	upstreamsTimeout    = 10 * time.Second
	upstreamsMax        = 128
	upstreamsConcurrent = 8
)

// upstreamCache collapses concurrent detections of one owner+directory,
// bounds how many run at once, and keeps a result for upstreamsTTL.
type upstreamCache struct {
	now     func() time.Time
	mu      sync.Mutex
	entries map[string]upstreamsEntry
	pending map[string]*upstreamsPending
	slots   chan struct{}
}

type upstreamsEntry struct {
	value     *hostsvc.ProjectUpstreams
	expiresAt time.Time
}

type upstreamsPending struct {
	done    chan struct{}
	ctx     context.Context
	cancel  context.CancelFunc
	waiters int
	value   *hostsvc.ProjectUpstreams
	err     error
}

// detectUpstreams asks the resolved owner to inspect its repository and
// returns only normalized repository/forge data to the hub.
func (m *Module) detectUpstreams(ctx context.Context, host hostsvc.Host, projectDir string) (string, []forge.Remote, error) {
	c := &m.upstreams
	key := host.RemoteID() + "\x00" + projectDir
	c.mu.Lock()
	cached, ok := c.entries[key]
	if ok && c.now().Before(cached.expiresAt) {
		c.mu.Unlock()
		return cached.value.RepoRoot, cached.value.Remotes, nil
	}
	if pending := c.pending[key]; pending != nil {
		pending.waiters++
		c.mu.Unlock()
		return c.wait(ctx, key, pending)
	}
	if err := ctx.Err(); err != nil {
		c.mu.Unlock()
		return "", nil, err
	}
	if c.slots == nil {
		c.slots = make(chan struct{}, upstreamsConcurrent)
	}
	slots := c.slots
	if c.pending == nil {
		c.pending = make(map[string]*upstreamsPending)
	}
	if len(c.pending) >= upstreamsMax {
		c.mu.Unlock()
		return "", nil, errors.New("too many upstream detections in progress")
	}
	pendingCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), upstreamsTimeout)
	pending := &upstreamsPending{done: make(chan struct{}), ctx: pendingCtx, cancel: cancel, waiters: 1}
	c.pending[key] = pending
	c.mu.Unlock()
	go m.loadUpstreams(key, projectDir, host, slots, pending)
	return c.wait(ctx, key, pending)
}

func (m *Module) loadUpstreams(key, projectDir string, host hostsvc.Host, slots chan struct{}, pending *upstreamsPending) {
	select {
	case slots <- struct{}{}:
		defer func() { <-slots }()
	case <-pending.ctx.Done():
		m.upstreams.finish(key, pending, nil, pending.ctx.Err())
		return
	}
	upstreams, err := host.ProjectUpstreams(pending.ctx, projectDir)
	if err == nil && upstreams == nil {
		err = errors.New("owner returned no upstream result")
	}
	if err == nil && !filepath.IsAbs(upstreams.RepoRoot) {
		err = errors.New("owner returned invalid repository root")
	}
	if err == nil {
		upstreams.Remotes = m.classify(upstreams.Remotes)
	}
	m.upstreams.finish(key, pending, upstreams, err)
}

func (c *upstreamCache) finish(key string, pending *upstreamsPending, upstreams *hostsvc.ProjectUpstreams, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	current := c.pending[key] == pending
	if err == nil && current {
		if c.entries == nil {
			c.entries = make(map[string]upstreamsEntry)
		}
		for len(c.entries) >= upstreamsMax {
			for oldKey := range c.entries {
				delete(c.entries, oldKey)
				break
			}
		}
		c.entries[key] = upstreamsEntry{value: upstreams, expiresAt: c.now().Add(upstreamsTTL)}
	}
	pending.value, pending.err = upstreams, err
	pending.cancel()
	if current {
		delete(c.pending, key)
	}
	close(pending.done)
}

func (c *upstreamCache) wait(ctx context.Context, key string, pending *upstreamsPending) (string, []forge.Remote, error) {
	defer func() {
		c.mu.Lock()
		pending.waiters--
		if pending.waiters == 0 && c.pending[key] == pending {
			delete(c.pending, key)
			pending.cancel()
		}
		c.mu.Unlock()
	}()
	select {
	case <-ctx.Done():
		return "", nil, ctx.Err()
	case <-pending.done:
		if pending.err != nil {
			return "", nil, pending.err
		}
		return pending.value.RepoRoot, pending.value.Remotes, nil
	}
}

func classifyRemotes(remotes []forge.Remote, knowsForgejo func(string) bool) []forge.Remote {
	out := make([]forge.Remote, 0, len(remotes))
	for _, rem := range remotes {
		rem.URL = ""
		if !validRemote(rem) {
			continue
		}
		switch {
		case rem.Host == "github.com":
			rem.Type = forge.RemoteTypeGitHub
		case knowsForgejo(rem.Host):
			rem.Type = forge.RemoteTypeForgejo
		default:
			continue
		}
		out = append(out, rem)
	}
	return out
}

func validRemote(rem forge.Remote) bool {
	if strings.TrimSpace(rem.Name) == "" || strings.TrimSpace(rem.Host) == "" || strings.ContainsAny(rem.Host, "/\\?#@") {
		return false
	}
	return forge.ValidRepo(rem.Repo)
}

// findRemote locates a remote by name in the detected list.
func findRemote(remotes []forge.Remote, name string) (forge.Remote, bool) {
	for _, r := range remotes {
		if r.Name == name {
			return r, true
		}
	}
	return forge.Remote{}, false
}

// handleUpstreams responds with the supported upstreams for the project
// containing dir.
//
// GET /api/project/upstreams?dir=<abs>&remoteId=<owner>
//
// 200: { "upstreams": [{remote, host, type, repo}, ...] }; 404: dir is not
// in a git repo; 400: dir or remoteId missing / dir not absolute.
func (m *Module) handleUpstreams(w http.ResponseWriter, r *http.Request) {
	dir, ok := httpx.AbsDir(w, r)
	if !ok {
		return
	}
	remoteID, ok := httpx.RemoteID(w, r.URL.Query().Get("remoteId"))
	if !ok {
		return
	}
	host, ok := m.Owner(w, dir, remoteID)
	if !ok {
		return
	}
	_, remotes, err := m.detectUpstreams(r.Context(), host, dir)
	if err != nil {
		if httpx.WriteCancellation(w, "failed to detect upstreams", err) {
			return
		}
		if errors.Is(err, git.ErrNotARepo) {
			http.Error(w, "directory is not a git repository", http.StatusNotFound)
			return
		}
		log.WithError(err).Warn("upstream: detect failed")
		http.Error(w, "failed to detect upstreams", http.StatusBadGateway)
		return
	}
	// Always emit a non-nil slice so the JSON is `[]` not `null`.
	if remotes == nil {
		remotes = []forge.Remote{}
	}
	httpx.WriteJSON(w, map[string]interface{}{"upstreams": remotes})
}
