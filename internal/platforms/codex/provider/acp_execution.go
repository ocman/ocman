package codexprovider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	acp "github.com/coder/acp-go-sdk"
	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/mcpauth"
	"github.com/ocman/ocman/internal/platforms"
	acpexec "github.com/ocman/ocman/internal/platforms/acp"
)

// acpExecution owns the sole execution connection. backend.conn remains a
// separate native reader; it must never load these threads for execution.
type acpExecution struct {
	b            *backend
	usage        rolloutUsageCache
	startMu      sync.Mutex
	mu           sync.Mutex
	client       *acpexec.Client
	loaded       map[string]bool
	loading      map[string]bool // verified roots whose resume notifications precede the response
	fresh        map[string]bool
	generation   uint64
	turns        map[string]*acpTurn
	children     map[string]acpChild // ACP ID to proven native identity
	childChanged chan struct{}       // wakes interactions queued behind native identity proof
}

type acpTurn struct {
	mu         sync.Mutex // converter updates and final native reconciliation
	admission  uint64
	conv       *Converter
	items      map[string]*acpItem
	started    int64
	baseUser   string
	baseTurn   string
	nativeID   string
	failed     bool
	cancelled  bool
	userSeeded bool
	command    bool
	title      string // requested title to persist once a fresh thread materializes
	// Native user seeding polls history off the shared ACP notification
	// goroutine. Items recorded meanwhile wait in buffered, in first-seen order.
	buffered   []string
	seedDone   chan struct{} // closed when seeding finishes; nil until started
	seedCancel context.CancelFunc
	err        error // session-scoped failure detected outside an ACP callback
}

func newACPExecution(b *backend) *acpExecution {
	return &acpExecution{b: b, loaded: map[string]bool{}, loading: map[string]bool{}, fresh: map[string]bool{}, turns: map[string]*acpTurn{}, children: map[string]acpChild{}, childChanged: make(chan struct{})}
}

func (a *acpExecution) conn(ctx context.Context) (*acpexec.Client, error) {
	a.startMu.Lock()
	defer a.startMu.Unlock()
	a.mu.Lock()
	c := a.client
	a.mu.Unlock()
	if c != nil {
		select {
		case <-c.Done():
			_ = c.Close()
			a.retireChildrenLocked(a.generation)
		default:
			return c, nil
		}
	}
	if a.b.ctx.Err() != nil {
		return nil, a.b.ctx.Err()
	}
	command := a.b.opts.ACPCommand
	if command.Executable == "" {
		command.Executable = "codex-acp"
	}
	env := a.b.opts.Env
	if env == nil {
		env = os.Environ()
	}
	env = append([]string{}, env...)
	if a.b.opts.CLI != "" {
		env = append(env, "CODEX_PATH="+a.b.opts.CLI)
	}
	if a.b.opts.Home != "" {
		env = append(env, "CODEX_HOME="+a.b.opts.Home)
	}
	a.mu.Lock()
	a.generation++
	generation := a.generation
	a.mu.Unlock()
	current := func() bool { a.mu.Lock(); defer a.mu.Unlock(); return a.generation == generation }
	c, err := acpexec.Start(ctx, acpexec.Options{Command: command, Dir: a.b.opts.Dir, Env: env,
		Version: a.b.opts.Version, Stderr: os.Stderr}, acpexec.Handlers{
		Update: func(ctx context.Context, p acp.SessionNotification) error {
			if !current() {
				return nil
			}
			return a.update(ctx, p)
		},
		Subagent: func(ctx context.Context, p acpexec.SubagentNotification) error {
			if !current() {
				return nil
			}
			return a.subagent(ctx, p)
		},
		Permission: func(ctx context.Context, p acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
			if !current() {
				return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeCancelled()}, nil
			}
			return a.permission(ctx, p)
		},
		Elicitation: func(ctx context.Context, p acpexec.ElicitationRequest) (acp.UnstableCreateElicitationResponse, error) {
			if !current() {
				return acp.UnstableCreateElicitationResponse{Cancel: &acp.UnstableCreateElicitationCancel{}}, nil
			}
			return a.question(ctx, p)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("codex ACP adapter unavailable; install @agentclientprotocol/codex-acp: %w", err)
	}
	if c.Info().AgentCapabilities.SessionCapabilities.Resume == nil {
		_ = c.Close()
		return nil, errors.New("codex ACP adapter does not support session resume")
	}
	a.mu.Lock()
	a.client, a.loaded = c, map[string]bool{}
	a.loading = map[string]bool{}
	a.children = map[string]acpChild{}
	close(a.childChanged)
	a.childChanged = make(chan struct{})
	a.mu.Unlock()
	go func() {
		<-c.Done()
		a.retireChildren(generation)
	}()
	return c, nil
}

func (a *acpExecution) close() {
	a.startMu.Lock()
	defer a.startMu.Unlock()
	a.mu.Lock()
	c := a.client
	a.mu.Unlock()
	if c != nil {
		_ = c.Close()
	}
}

func (a *acpExecution) servers() []acp.McpServer {
	if a.b.opts.MCPAddr == "" {
		return []acp.McpServer{}
	}
	headers := []acp.HttpHeader{}
	if token := mcpauth.ReadToken(a.b.opts.MCPTokenFile); token != "" {
		headers = append(headers, acp.HttpHeader{Name: "Authorization", Value: mcpauth.Header(token)})
	}
	return []acp.McpServer{{Http: &acp.McpServerHttpInline{Name: "ocman", Url: "http://" + a.b.opts.MCPAddr + "/mcp", Headers: headers}}}
}

func (a *acpExecution) create(ctx context.Context, req platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
	if err := absoluteACPDirectory(req.Directory); err != nil {
		return nil, errors.Join(platforms.ErrCreationRejected, statusError{http.StatusBadRequest, err})
	}
	c, err := a.conn(ctx)
	if err != nil {
		return nil, errors.Join(platforms.ErrCreationRejected, err)
	}
	resp, err := c.NewSession(ctx, acp.NewSessionRequest{Cwd: req.Directory, McpServers: a.servers()})
	if err != nil {
		return nil, err // allocation may have happened; never replay
	}
	id := string(resp.SessionId)
	if id == "" {
		return nil, errors.New("codex ACP allocated an empty session ID")
	}
	a.mu.Lock()
	if a.client != c {
		a.mu.Unlock()
		return nil, errors.New("codex ACP exited after allocating session " + id)
	}
	a.loaded[id] = true
	a.fresh[id] = true
	a.mu.Unlock()
	a.b.mu.Lock()
	s := a.b.sessionLocked(id)
	s.dir, s.title = req.Directory, req.Title
	s.provider = "openai"
	a.b.mu.Unlock()
	return &platforms.CreateSessionResponse{ID: id}, nil
}

func (a *acpExecution) resume(ctx context.Context, c *acpexec.Client, id string) error {
	a.mu.Lock()
	child := false
	for _, entry := range a.children {
		child = child || entry.native == id
	}
	a.mu.Unlock()
	if child {
		return platforms.ErrUnsupported
	}
	a.b.mu.Lock()
	s := a.b.sessionLocked(id)
	a.b.mu.Unlock()
	s.loadMu.Lock()
	defer s.loadMu.Unlock()
	a.mu.Lock()
	if a.client != c {
		a.mu.Unlock()
		return errors.New("codex ACP connection changed before resume")
	}
	loaded := a.loaded[id]
	a.mu.Unlock()
	if loaded {
		return nil
	}
	native, _, err := a.b.conn(ctx)
	if err != nil {
		return err
	}
	thread, err := native.ReadThread(ctx, id, false)
	if err != nil {
		return notFound(err)
	}
	if thread.ID != id {
		return errors.New("codex history returned a different thread")
	}
	if thread.ParentThreadID != "" {
		return platforms.ErrUnsupported
	}
	a.mu.Lock()
	a.loading[id] = true
	a.mu.Unlock()
	defer func() { a.mu.Lock(); delete(a.loading, id); a.mu.Unlock() }()
	_, err = c.ResumeSession(ctx, acp.ResumeSessionRequest{SessionId: acp.SessionId(id), Cwd: thread.Cwd, McpServers: a.servers()})
	if err != nil {
		return threadLoadError(err) // ownership rejection never authorizes a native resume
	}
	a.b.mu.Lock()
	s = a.b.sessionLocked(id)
	s.dir, s.provider, s.model = thread.Cwd, thread.ModelProvider, thread.Model
	a.b.mu.Unlock()
	a.mu.Lock()
	if a.client != c {
		a.mu.Unlock()
		return errors.New("codex ACP connection changed during resume")
	}
	a.loaded[id] = true
	a.mu.Unlock()
	return nil
}

func absoluteACPDirectory(dir string) error {
	// Keep validation shared with platform creation without launching either
	// the adapter or native reader for malformed requests.
	if !filepath.IsAbs(dir) {
		return errors.New("directory must be absolute")
	}
	return nil
}

func (a *acpExecution) abort(ctx context.Context, id string) error {
	a.mu.Lock()
	c, turn := a.client, a.turns[id]
	a.mu.Unlock()
	if c == nil || turn == nil {
		return nil
	}
	return c.Cancel(ctx, acp.SessionId(id))
}

func (a *acpExecution) settle(id string, turn *acpTurn, err error, cancelled bool) {
	status := db.StatusDone
	if err != nil {
		a.b.errorMessage(id, err.Error())
		status = db.StatusError
	} else if turn.failed {
		status = db.StatusError
	} else if cancelled || turn.cancelled {
		status = db.StatusInterrupted
	}
	a.b.settle(id, status, turn.admission)
}
