package codexappserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"
)

// Options configures a spawned `codex app-server`.
type Options struct {
	CLI    string   // codex executable
	Args   []string // extra arguments, such as -c key=value overrides
	Env    []string // full child environment; nil inherits this process's
	Dir    string   // working directory of the server process
	Client ClientInfo
	// ExperimentalAPI enables gated methods and fields, such as
	// item/tool/requestUserInput.
	ExperimentalAPI bool
	// OptOut suppresses these notification methods on the connection.
	OptOut []string
	Stderr io.Writer
}

// Client is an initialized connection to one app-server process.
type Client struct {
	*Conn
	Info InitializeResponse

	cmd      *exec.Cmd
	stdin    io.Closer
	waitOnce sync.Once
	exited   chan struct{} // closed once cmd.Wait returns; waitErr is then set
	waitErr  error
}

// Start spawns `codex app-server`, performs the initialize handshake and
// returns the ready client. ctx bounds only the handshake; Close stops the
// process.
func Start(ctx context.Context, opts Options, handler Handler) (*Client, error) {
	if opts.CLI == "" {
		opts.CLI = "codex"
	}
	cmd := exec.Command(opts.CLI, append([]string{"app-server"}, opts.Args...)...)
	cmd.Env, cmd.Dir, cmd.Stderr = opts.Env, opts.Dir, opts.Stderr
	// A killed server's descendants may hold stderr open; do not wait on them.
	cmd.WaitDelay = time.Second
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting codex app-server: %w", err)
	}
	c := &Client{Conn: NewConn(stdout, stdin, handler), cmd: cmd, stdin: stdin}
	if err := c.initialize(ctx, opts); err != nil {
		_ = c.stop(failedStartGrace)
		return nil, err
	}
	return c, nil
}

// Attach wraps an existing pipe pair, such as an in-process fake, and
// performs the handshake.
func Attach(ctx context.Context, r io.Reader, w io.WriteCloser, opts Options, handler Handler) (*Client, error) {
	c := &Client{Conn: NewConn(r, w, handler), stdin: w}
	if err := c.initialize(ctx, opts); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}

func (c *Client) initialize(ctx context.Context, opts Options) error {
	if opts.Client.Name == "" {
		opts.Client = ClientInfo{Name: "ocman", Version: "dev"}
	}
	params := InitializeParams{ClientInfo: opts.Client, Capabilities: &InitializeCapabilities{
		ExperimentalAPI: opts.ExperimentalAPI, OptOutNotificationMethods: opts.OptOut}}
	if err := c.Call(ctx, MethodInitialize, params, &c.Info); err != nil {
		return fmt.Errorf("codex app-server initialize: %w", err)
	}
	return c.Notify(MethodInitialized, nil)
}

// How long a closed server may take to exit on stdin EOF before it is killed.
// A server that failed its handshake gets less.
const (
	closeGrace       = 5 * time.Second
	failedStartGrace = 500 * time.Millisecond
)

// Close ends the connection and waits for the process to exit. Closing stdin
// is the server's shutdown signal; a server still running after closeGrace is
// killed and reaped.
func (c *Client) Close() error {
	return c.stop(closeGrace)
}

func (c *Client) stop(grace time.Duration) error {
	err := c.stdin.Close()
	if c.cmd == nil {
		return err
	}
	c.waitOnce.Do(func() {
		c.exited = make(chan struct{})
		go func() { c.waitErr = c.cmd.Wait(); close(c.exited) }()
	})
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-c.exited:
	case <-timer.C:
		_ = c.cmd.Process.Kill()
		<-c.exited
	}
	var exit *exec.ExitError
	if errors.As(c.waitErr, &exit) {
		return nil // a nonzero exit after stdin closed is the server's choice
	}
	return c.waitErr
}

func (c *Client) ReadThread(ctx context.Context, id string, turns bool) (*Thread, error) {
	var out ThreadReadResponse
	if err := c.Call(ctx, MethodThreadRead, ThreadReadParams{ThreadID: id, IncludeTurns: turns}, &out); err != nil {
		return nil, err
	}
	return &out.Thread, nil
}

// page is the common shape of paged list responses.
type page[T any] struct {
	Data       []T     `json:"data"`
	NextCursor *string `json:"nextCursor"`
}

// collect follows nextCursor until the last page. A repeated cursor is an
// error rather than an endless loop.
func collect[T any](ctx context.Context, c *Client, method string, params func(cursor string) any) ([]T, error) {
	var all []T
	seen := map[string]bool{}
	cursor := ""
	for {
		var p page[T]
		if err := c.Call(ctx, method, params(cursor), &p); err != nil {
			return nil, err
		}
		all = append(all, p.Data...)
		if p.NextCursor == nil || *p.NextCursor == "" {
			return all, nil
		}
		if seen[*p.NextCursor] {
			return nil, fmt.Errorf("codex %s repeated cursor %q", method, *p.NextCursor)
		}
		seen[*p.NextCursor] = true
		cursor = *p.NextCursor
	}
}

// ListThreads returns every page of threads matching p, ignoring p.Cursor.
func (c *Client) ListThreads(ctx context.Context, p ThreadListParams) ([]Thread, error) {
	return collect[Thread](ctx, c, MethodThreadList, func(cursor string) any { p.Cursor = cursor; return p })
}

// ListTurns returns every turn of a thread, oldest first, with itemsView
// ("notLoaded", "summary" or "full").
func (c *Client) ListTurns(ctx context.Context, threadID, itemsView string) ([]Turn, error) {
	return collect[Turn](ctx, c, MethodThreadTurns, func(cursor string) any {
		return ThreadTurnsListParams{ThreadID: threadID, Cursor: cursor, SortDirection: "asc", ItemsView: itemsView}
	})
}

// ListItems returns every stored item of a thread, oldest first.
func (c *Client) ListItems(ctx context.Context, threadID string) ([]ItemEntry, error) {
	return collect[ItemEntry](ctx, c, MethodThreadItems, func(cursor string) any {
		return ThreadItemsListParams{ThreadID: threadID, Cursor: cursor, SortDirection: "asc"}
	})
}

// ListSkills lists the skills available in each directory.
func (c *Client) ListSkills(ctx context.Context, cwds ...string) ([]SkillsEntry, error) {
	var out SkillsListResponse
	if err := c.Call(ctx, MethodSkillsList, SkillsListParams{Cwds: cwds}, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

func (c *Client) ReadAccount(ctx context.Context) (*AccountResponse, error) {
	var out AccountResponse
	return &out, c.Call(ctx, MethodAccountRead, struct{}{}, &out)
}

func (c *Client) ReadRateLimits(ctx context.Context) (*RateLimitSnapshot, error) {
	var out RateLimitsResponse
	if err := c.Call(ctx, MethodRateLimits, struct{}{}, &out); err != nil {
		return nil, err
	}
	return &out.RateLimits, nil
}

func (c *Client) ArchiveThread(ctx context.Context, id string) error {
	return c.Call(ctx, MethodThreadArchive, ThreadIDParams{ThreadID: id}, nil)
}

func (c *Client) SetThreadName(ctx context.Context, id, name string) error {
	return c.Call(ctx, MethodThreadNameSet, ThreadNameSetParams{ThreadID: id, Name: name}, nil)
}

// ListModels returns every page of the model catalog.
func (c *Client) ListModels(ctx context.Context, includeHidden bool) ([]Model, error) {
	return collect[Model](ctx, c, MethodModelList, func(cursor string) any {
		return ModelListParams{Cursor: cursor, IncludeHidden: includeHidden}
	})
}
