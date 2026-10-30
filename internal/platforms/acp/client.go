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
	updateErr error
}

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
	c := &Client{proc: p}
	c.conn = newWire(callbacks{handlers: handlers, failed: c.failUpdate}, p.stdin, p.stdout)
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
