// Package provider adapts an owner-local backend socket to platforms.Platform.
package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/providerprotocol"
)

var (
	_ platforms.Integration         = (*Adapter)(nil)
	_ platforms.LifecycleReader     = (*Adapter)(nil)
	_ platforms.NativeQueue         = (*Adapter)(nil)
	_ platforms.PermissionRefresher = (*Adapter)(nil)
	_ platforms.SessionEventSource  = (*Adapter)(nil)
)

type Adapter struct {
	hello             providerprotocol.Hello
	client            *http.Client
	mu                sync.RWMutex
	observeMu         sync.Mutex      // keep lifecycle callbacks in sequence across consumers
	known             map[string]bool // session IDs seen in listings, details or events
	live              map[string]platforms.LiveState
	permissions       map[string]platforms.LivePrompt
	questions         map[string]platforms.LivePrompt
	seq               uint64
	promptVersion     uint64
	headlessConnected bool
	sink              platforms.SessionEventSink
	onEvent           func(providerprotocol.Event)
	acks              map[string]uint64
	rules             *Rules          // ocman-owned rules; nil uses the provider's rules RPC
	ruled             map[string]bool // prompts answered by rules, until their reply
	ctx               context.Context // the adapter's lifetime, ended by Close
	draining          bool            // NewDraining: an out-of-range supervisor being replaced
	stop              context.CancelFunc
}

// New negotiates the protocol on socket. No goroutine is started implicitly;
// runtime management owns the lifetime of WatchEvents and the adapter.
func New(ctx context.Context, socket string) (*Adapter, error) {
	return connect(ctx, socket, false)
}

// NewDraining connects to a supervisor outside the supported protocol range
// while it drains for replacement (AD-5). Only its version-independent
// routes are used: the headless event stream, of which only prompt events
// (permission.*, question.*) are read, pending prompt lists, prompt replies
// and abort. So a prompt it holds, or asks later in the same turn, reaches
// the dashboard pipeline and stays answerable, and the drain can finish.
// Nothing else is read from it: no acknowledgments, snapshots, sessions,
// lifecycle events or catalogs.
func NewDraining(ctx context.Context, socket string) (*Adapter, error) {
	return connect(ctx, socket, true)
}

func connect(ctx context.Context, socket string, draining bool) (*Adapter, error) {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		},
		ResponseHeaderTimeout: 10 * time.Second,
	}
	a := &Adapter{
		client: &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		known:  make(map[string]bool), live: make(map[string]platforms.LiveState),
		permissions: make(map[string]platforms.LivePrompt), questions: make(map[string]platforms.LivePrompt), ruled: make(map[string]bool),
	}
	var handshake struct {
		providerprotocol.Hello
		Version *int `json:"protocolVersion"`
	}
	if err := a.call(ctx, true, "GET", "/hello", nil, &handshake); err != nil {
		transport.CloseIdleConnections()
		return nil, err
	}
	a.hello = handshake.Hello
	if handshake.Version == nil || a.hello.CLIVersion == "" {
		transport.CloseIdleConnections()
		return nil, fmt.Errorf("provider /hello omitted protocolVersion or cliVersion")
	}
	a.hello.ProtocolVersion = *handshake.Version
	if draining {
		a.draining = true
		a.hello.Capabilities = providerprotocol.Capabilities{Capabilities: platforms.Capabilities{
			RespondPermission: true, RespondQuestion: true, Abort: true, Events: true}}
	} else if err := providerprotocol.CheckVersion(a.hello.ProtocolVersion); err != nil {
		transport.CloseIdleConnections()
		return nil, err
	}
	if a.hello.PlatformID == "" {
		transport.CloseIdleConnections()
		return nil, fmt.Errorf("provider /hello omitted providerId")
	}
	a.ctx, a.stop = context.WithCancel(context.WithoutCancel(ctx))
	return a, nil
}

func (a *Adapter) Close() {
	if a.stop != nil {
		a.stop()
	}
	a.client.CloseIdleConnections()
}
func (a *Adapter) ID() platforms.ID    { return platforms.ID(a.hello.PlatformID) }
func (a *Adapter) DisplayName() string { return a.hello.PlatformID }

// Capabilities always reports ExactPatterns: every provider's patterns and
// rules use providerprotocol's escapes, the semantics Rules.Decide applies.
func (a *Adapter) Capabilities() platforms.Capabilities {
	caps := a.hello.Capabilities.Capabilities
	caps.ExactPatterns = true
	return caps
}
func (a *Adapter) Hello() providerprotocol.Hello { return a.hello }

// ReplyOutcomes is false for a (draining) supervisor that predates
// CodeReplyUnrecorded: its generic 503 may follow a delivered reply.
func (a *Adapter) ReplyOutcomes(context.Context, string) bool {
	return a.hello.ProtocolVersion >= providerprotocol.ReplyOutcomeVersion
}
func (a *Adapter) Available(ctx context.Context) bool {
	var hello providerprotocol.Hello
	return a.call(ctx, true, "GET", "/hello", nil, &hello) == nil &&
		hello.PlatformID == a.hello.PlatformID && providerprotocol.CheckVersion(hello.ProtocolVersion) == nil
}

func (a *Adapter) call(ctx context.Context, supported bool, method, path string, body, out any) error {
	if !supported || (a.draining && !drainingRoute(method, path)) {
		return platforms.ErrUnsupported
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	resp, err := a.request(ctx, method, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out == nil {
		_, err = io.Copy(io.Discard, io.LimitReader(resp.Body, 32<<20))
		return err
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(out); err != nil {
		return fmt.Errorf("provider %s: decode response: %w", path, err)
	}
	return nil
}

// drainingRoute lists the version-independent routes a draining adapter uses.
func drainingRoute(method, path string) bool {
	path, _, _ = strings.Cut(path, "?")
	switch {
	case method == "GET":
		return path == "/hello" || path == "/events" || path == "/permissions" || path == "/questions"
	case strings.HasPrefix(path, "/permissions/"), strings.HasPrefix(path, "/questions/"):
		return true
	}
	return strings.HasPrefix(path, "/sessions/") && strings.HasSuffix(path, "/abort")
}

func (a *Adapter) request(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var input io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		input = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://provider"+path, input)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", platforms.ErrPlatformUnreachable, err)
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return resp, nil
	}
	defer resp.Body.Close()
	var wire providerprotocol.Error
	_ = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&wire)
	return nil, providerResponseError(resp.StatusCode, wire)
}

func sessionPath(id, suffix string) string { return "/sessions/" + url.PathEscape(id) + suffix }

func (a *Adapter) remember(s db.Session) {
	a.mu.Lock()
	a.known[s.ID] = true
	a.mu.Unlock()
}

func (a *Adapter) Sessions(ctx context.Context, dir string, since int64) ([]db.Session, error) {
	var sessions []db.Session
	err := a.call(ctx, true, "GET", "/sessions?"+url.Values{"directory": {dir}, "since": {fmt.Sprint(since)}}.Encode(), nil, &sessions)
	if err != nil {
		return nil, err
	}
	for i := range sessions {
		sessions[i].Platform = string(a.ID())
		a.remember(sessions[i])
	}
	return sessions, nil
}

// RuntimeDetail reads the supervisor's own page of a session it serves. It
// routes execution and seeds identity metadata for a running session before
// its transcript exists; conversations are read from statev2.
func (a *Adapter) RuntimeDetail(ctx context.Context, id string, limit, offset int) (*platforms.SessionDetail, error) {
	var detail platforms.SessionDetail
	err := a.call(ctx, true, "GET", sessionPath(id, fmt.Sprintf("?limit=%d&offset=%d", limit, offset)), nil, &detail)
	if err != nil {
		return nil, err
	}
	if detail.Session == nil || detail.Session.ID != id {
		return nil, fmt.Errorf("provider returned invalid session detail for %q", id)
	}
	detail.Session.Platform = string(a.ID())
	a.remember(*detail.Session)
	for i := range detail.SessionTree {
		detail.SessionTree[i].Platform = string(a.ID())
		a.remember(detail.SessionTree[i])
	}
	return &detail, nil
}

// Knows reports whether this adapter has observed the session in a listing,
// detail or event. It never makes a provider call.
func (a *Adapter) Knows(id string) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.known[id]
}

func (a *Adapter) SessionLifecycle(ctx context.Context, id string) (*platforms.SessionLifecycle, error) {
	var result platforms.SessionLifecycle
	err := a.call(ctx, true, "GET", sessionPath(id, "/lifecycle"), nil, &result)
	return &result, err
}

func (a *Adapter) Account(ctx context.Context) (*providerprotocol.Account, error) {
	var result providerprotocol.Account
	err := a.call(ctx, a.hello.Capabilities.Account, "GET", "/account", nil, &result)
	return &result, err
}

func (a *Adapter) LiveStatus(id string) *platforms.LiveState {
	a.mu.RLock()
	defer a.mu.RUnlock()
	s, ok := a.live[id]
	if !ok {
		return nil
	}
	for _, p := range a.permissions {
		if p["sessionID"] == id {
			s.PendingPermission = true
		}
	}
	for _, p := range a.questions {
		if p["sessionID"] == id {
			s.PendingQuestion = true
		}
	}
	return &s
}

// LiveDirectoryCatalog reads the running CLI's agent, command and model
// configuration for a directory. It carries no session history.
func (a *Adapter) LiveDirectoryCatalog(ctx context.Context, req platforms.DirectoryCatalogRequest) (*platforms.DirectoryCatalog, error) {
	var result platforms.DirectoryCatalog
	err := a.call(ctx, true, "GET", "/catalog?"+url.Values{"directory": {req.Directory}}.Encode(), nil, &result)
	return &result, err
}
