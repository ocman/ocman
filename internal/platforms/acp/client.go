package acpexec

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"time"

	acp "github.com/coder/acp-go-sdk"
)

// Options describes a process on the owning host. Env is the complete child
// environment; nil inherits the parent's. Config/home isolation belongs to the
// caller, as does routing remote launches to their owner.
type Options struct {
	Command Command
	Dir     string
	Env     []string
	Stderr  io.Writer
	Logger  *slog.Logger
	Version string
}

// Client owns one ACP v1 connection and process. It may serve multiple sessions.
// There is deliberately no automatic retry, native fallback or session-ID
// conversion: a failed mutation may already have reached the agent.
type Client struct {
	conn      *wire
	info      acp.InitializeResponse
	proc      *process
	mu        sync.Mutex
	updateErr error // transport-wide recording failure
	sessions  map[acp.SessionId]*sessionState

	beforeFailureCancel func() // test seam: runs before a failure cancellation is sent
}

// sessionState tracks a session's in-flight operations. ACP cancellation is
// session-scoped, so the cancellation issued for a failed prompt is fenced: the
// failed prompt does not return, and no further operation on the session is
// admitted, until that cancellation has been sent or skipped.
type sessionState struct {
	ops     map[*sessionOp]struct{}
	pending error         // recording failure outside any operation
	fence   chan struct{} // closed when an outstanding failure cancellation ends
}

// sessionOp is one in-flight operation. Updates recorded while it runs belong
// to it.
type sessionOp struct {
	prompt  bool
	settled bool          // the agent answered; a cancellation can no longer target it
	err     error         // first recording failure during the operation
	fence   chan struct{} // the failure cancellation issued for this prompt
}

// sessionCancelTimeout bounds the cancellation sent after a session's update
// could not be recorded.
const sessionCancelTimeout = 10 * time.Second

// Start launches the supplied executable and negotiates ACP v1. ctx bounds the
// handshake only. The caller must Close the client when its supervisor stops.
func Start(ctx context.Context, opts Options, handlers Handlers) (*Client, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if opts.Command.Executable == "" {
		return nil, fmt.Errorf("ACP executable is required")
	}
	if handlers.Update == nil {
		return nil, fmt.Errorf("ACP session update handler is required")
	}
	p, err := launch(opts)
	if err != nil {
		return nil, err
	}
	c := &Client{proc: p, sessions: map[acp.SessionId]*sessionState{}}
	c.conn = newWire(callbacks{handlers: handlers, failed: c.failUpdate, sessionFailed: c.failSessionUpdate}, p.stdin, p.stdout)
	if opts.Logger != nil {
		c.conn.SetLogger(opts.Logger)
	}
	if err := c.initialize(ctx, opts.Version); err != nil {
		p.stop(100 * time.Millisecond)
		return nil, err
	}
	return c, nil
}

func (c *Client) initialize(ctx context.Context, version string) error {
	if version == "" {
		version = "dev"
	}
	capabilities := acp.ClientCapabilities{}
	if c.conn.callbacks.handlers.Elicitation != nil {
		capabilities.Elicitation = &acp.ElicitationCapabilities{Form: &acp.ElicitationFormCapabilities{}}
	}
	info, err := c.conn.Initialize(ctx, acp.InitializeRequest{
		ProtocolVersion: acp.ProtocolVersionNumber,
		ClientInfo:      &acp.Implementation{Name: "ocman", Version: version},
		// File and terminal operations remain agent-owned. Do not advertise
		// host capabilities until they have owner-routed implementations.
		ClientCapabilities: capabilities,
	})
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("ACP initialize: %w", ctx.Err())
		}
		return fmt.Errorf("ACP initialize: %w", err)
	}
	if info.ProtocolVersion != acp.ProtocolVersionNumber {
		return fmt.Errorf("unsupported ACP protocol version %d", info.ProtocolVersion)
	}
	c.info = info
	return nil
}

// Info returns the negotiated capabilities, authentication methods and identity.
// The returned data must be treated as read-only.
func (c *Client) Info() acp.InitializeResponse { return c.info }

// Done closes on transport disconnect, not on completion of a prompt.
func (c *Client) Done() <-chan struct{} { return c.conn.Done() }

// A failed event sink must never look like a successfully recorded turn. End
// the transport without retrying; the supervisor must reconcile native history.
func (c *Client) failUpdate(err error) {
	c.mu.Lock()
	if c.updateErr == nil {
		c.updateErr = err
	}
	c.mu.Unlock()
	c.proc.disconnect()
}

// A session's failed event sink must not end other sessions' turns on the
// shared adapter. Fail only that session's active operations and cancel its
// unsettled prompt; transport failures still end the connection through Done.
// A failure outside any operation cancels nothing: no prompt is running that
// the session-scoped cancellation could safely target.
func (c *Client) failSessionUpdate(sessionID acp.SessionId, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.session(sessionID)
	if len(s.ops) == 0 {
		if s.pending == nil {
			s.pending = err
		}
		return
	}
	for op := range s.ops {
		if op.err != nil {
			continue
		}
		op.err = err
		if op.prompt && !op.settled && op.fence == nil {
			op.fence = make(chan struct{})
			s.fence = op.fence
			// Never write to the peer from the shared notification goroutine.
			go c.cancelFailedPrompt(sessionID, op, c.beforeFailureCancel)
		}
	}
}

// cancelFailedPrompt cancels op's turn unless it has already settled, then
// releases the fence holding back the prompt's return and the next operation.
// Because nothing else on the session is admitted meanwhile, the cancellation
// can only reach the prompt generation it was issued for.
func (c *Client) cancelFailedPrompt(sessionID acp.SessionId, op *sessionOp, before func()) {
	if before != nil {
		before()
	}
	c.mu.Lock()
	superseded := op.settled
	c.mu.Unlock()
	if !superseded {
		ctx, cancel := context.WithTimeout(context.Background(), sessionCancelTimeout)
		_ = c.conn.Cancel(ctx, acp.CancelNotification{SessionId: sessionID})
		cancel()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	close(op.fence)
	s := c.sessions[sessionID]
	if s.fence == op.fence {
		s.fence = nil
	}
	c.release(sessionID, s)
}

// session returns the state for sessionID. c.mu must be held.
func (c *Client) session(sessionID acp.SessionId) *sessionState {
	s := c.sessions[sessionID]
	if s == nil {
		s = &sessionState{ops: map[*sessionOp]struct{}{}}
		c.sessions[sessionID] = s
	}
	return s
}

// release forgets an idle session. c.mu must be held.
func (c *Client) release(sessionID acp.SessionId, s *sessionState) {
	if len(s.ops) == 0 && s.pending == nil && s.fence == nil {
		delete(c.sessions, sessionID)
	}
}

// beginSessionOp admits an operation once any outstanding failure cancellation
// on the session has ended, so that cancellation cannot reach the new operation.
// An unreported failure from outside any operation fails this one instead of
// being discarded.
func (c *Client) beginSessionOp(ctx context.Context, sessionID acp.SessionId, prompt bool) (*sessionOp, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for {
		s := c.session(sessionID)
		if s.fence == nil {
			if failed := s.pending; failed != nil {
				s.pending = nil
				c.release(sessionID, s)
				return nil, fmt.Errorf("recording ACP update: %w", failed)
			}
			op := &sessionOp{prompt: prompt}
			s.ops[op] = struct{}{}
			return op, nil
		}
		fence := s.fence
		c.mu.Unlock()
		select {
		case <-fence:
			c.mu.Lock()
		case <-ctx.Done():
			c.mu.Lock()
			c.release(sessionID, c.session(sessionID))
			return nil, ctx.Err()
		}
	}
}

// endSessionOp settles op, waits for a failure cancellation issued for it and
// returns the first update that could not be recorded while it ran.
func (c *Client) endSessionOp(sessionID acp.SessionId, op *sessionOp) error {
	c.mu.Lock()
	op.settled = true
	fence := op.fence
	c.mu.Unlock()
	if fence != nil {
		<-fence // bounded by sessionCancelTimeout or the transport closing
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.sessions[sessionID]
	delete(s.ops, op)
	c.release(sessionID, s)
	if op.err != nil {
		return fmt.Errorf("recording ACP update: %w", op.err)
	}
	return nil
}

// Close closes the pipes, then waits briefly before killing and reaping a
// process that ignores stdin EOF. It is safe to call concurrently or repeatedly.
func (c *Client) Close() error {
	c.proc.stop(2 * time.Second)
	return nil
}

type process struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *os.File
	exited chan struct{}
	once   sync.Once
}

func launch(opts Options) (*process, error) {
	cmd := exec.Command(opts.Command.Executable, opts.Command.Args...)
	cmd.Dir, cmd.Env, cmd.Stderr = opts.Dir, opts.Env, opts.Stderr
	cmd.WaitDelay = time.Second
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	// Own the read end ourselves so cmd.Wait cannot close it while the ACP
	// SDK is still draining final responses.
	stdout, output, err := os.Pipe()
	if err != nil {
		_ = stdin.Close()
		return nil, err
	}
	cmd.Stdout = output
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = output.Close()
		return nil, fmt.Errorf("starting ACP executable %q: %w", opts.Command.Executable, err)
	}
	_ = output.Close()
	p := &process{cmd: cmd, stdin: stdin, stdout: stdout, exited: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		close(p.exited)
	}()
	return p, nil
}

func (p *process) stop(grace time.Duration) {
	p.once.Do(func() {
		p.disconnect()
		timer := time.NewTimer(grace)
		defer timer.Stop()
		select {
		case <-p.exited:
		case <-timer.C:
			_ = p.cmd.Process.Kill()
			<-p.exited
		}
	})
}

func (p *process) disconnect() {
	_ = p.stdin.Close()
	_ = p.stdout.Close()
}
