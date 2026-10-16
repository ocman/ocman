package claudeprovider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/NoUseFreak/ocman/internal/claudecli"
)

// proc is one live claude child, driven over stream-json pipes (AD-6).
type proc struct {
	id     string
	model  string
	effort string
	mcp    string // the --mcp-config it was launched with
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	busy   bool // a turn is open: never evicted or timed out
	// pending holds inputs written while a turn was open that the CLI has not
	// started (no replay echo yet). A result is the idle edge only once it is
	// empty: a queued input produces its own result later (spike §3).
	pending map[string]bool
	aborted bool // an interrupt was sent: the turn's result is not a failure
	replied bool // the CLI sent assistant output since the last result
	// startErr latches a failed ownership update at the replay echo.
	startErr error
	idle     *time.Timer // started when a turn completes
	conv     *Converter  // touched only by the reader goroutine
	// children maps a spawning Task/Agent call id to its subagent's
	// converter (reader goroutine only).
	children       map[string]*Converter
	dir            string // working directory
	stderr         *tail
	done           chan struct{} // closed once the child has been reaped
	release        func()        // frees the machine slot once the child is reaped; under l.mu
	writeMu        sync.Mutex
	controlMu      sync.Mutex
	controls       map[string]chan controlResult
	initializeOnce sync.Once
	initializeErr  error
	initialized    bool            // under controlMu
	state          string          // Claude's session_state_changed; empty on older CLIs
	tasks          map[string]bool // delegated agents/workflows that can owe another turn
	deferredResult []byte
	deferredGen    uint64
	runCeiling     *time.Timer
}

func (p *proc) freeSlotLocked() {
	if p.release != nil {
		p.release()
		p.release = nil
	}
}

// tail keeps the last few KiB of stderr for error messages.
type tail struct {
	mu  sync.Mutex
	buf []byte
}

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > 4096 {
		t.buf = t.buf[len(t.buf)-4096:]
	}
	return len(p), nil
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.TrimSpace(string(t.buf))
}

// launchArgs is the AD-6 command. --verbose is required by stream-json output;
// subagent forwarding is requested in the SDK initialization handshake.
func (l *live) launchArgs(id string, o owned, model, effort, mcp string) []string {
	args := []string{"-p", "--verbose", "--input-format", "stream-json", "--output-format", "stream-json",
		"--include-partial-messages", "--replay-user-messages",
		// Every tool call the CLI does not settle from the user's own Claude
		// settings is asked of ocman over the control protocol (AD-7).
		"--permission-mode", "default", "--permission-prompt-tool", "stdio"}
	if model != "" {
		args = append(args, "--model", model)
	}
	if effort != "" {
		args = append(args, "--effort", effort)
	}
	if mcp != "" {
		args = append(args, "--mcp-config", mcp)
	}
	if l.opts.PluginDir != "" {
		args = append(args, "--plugin-dir", l.opts.PluginDir)
	}
	switch {
	case o.ForkOf != "":
		return append(args, "--resume", o.ForkOf, "--fork-session", "--session-id", id)
	case o.Started:
		return append(args, "--resume", id)
	}
	return append(args, "--session-id", id)
}

// errAborted ends a turn the user aborted before it reached a child.
var errAborted = errors.New("aborted before start")

// waitTicket is one turn between BeginTurn and writing its input; abort
// cancels it, so a turn waiting for a slot never starts later.
type waitTicket struct {
	input     string // the user message this turn writes
	cancelled bool
	written   bool // the input reached the child's stdin (under l.mu)
}

// acquire returns the session's child marked busy, spawning (or resuming) it.
// At the cap an idle child is evicted; when every child is busy it waits.
// input is recorded as pending when it joins an open turn.
func (l *live) acquire(ctx context.Context, id, model, effort, input string, w *waitTicket) (*proc, error) {
	// A waiter on l.slots wakes when ctx ends.
	stop := context.AfterFunc(ctx, func() { l.mu.Lock(); l.slots.Broadcast(); l.mu.Unlock() })
	defer stop()
	l.mu.Lock()
	defer l.mu.Unlock()
	var waiter *os.File
	defer func() { dropWaiter(waiter) }()
	for {
		if w.cancelled {
			return nil, errAborted
		}
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("claude provider stopping: %w", err)
		}
		if p := l.procs[id]; p != nil {
			// Model changes use set_model. Effort is launch-only, and a rebound
			// MCP listener also restarts only an idle child.
			if p.busy || (effort == p.effort && p.mcp == l.opts.mcpConfig()) {
				if p.busy {
					p.pending[input] = true
				}
				p.busy = true
				if p.idle != nil {
					p.idle.Stop()
				}
				return p, nil
			}
			l.stopLocked(p)
		}
		release, ok, err := l.takeSlotLocked()
		if err != nil {
			return nil, err // a broken slot store fails the turn; it is not contention
		}
		if ok {
			p, err := l.spawnLocked(id, model, effort)
			if err != nil {
				release()
				return nil, err
			}
			p.release = release
			return p, nil
		}
		evicted := false
		for _, p := range l.procs {
			if !p.busy && !l.exitingLocked() {
				l.stopLocked(p)
				evicted = true
				break
			}
		}
		if !evicted || l.exitingLocked() {
			l.waitSlotLocked(ctx, &waiter) // the evicted child frees its slot when reaped
		}
	}
}

func (l *live) spawnLocked(id, model, effort string) (*proc, error) {
	l.syncOwnedLocked()
	if l.unrecorded[id] {
		// Claude wrote a transcript ocman could not record: record it now,
		// or fail visibly; never launch it again as a fresh --session-id.
		if err := l.markStartedLocked(id); err != nil {
			return nil, fmt.Errorf("session has a transcript ocman could not record: %w", err)
		}
		delete(l.unrecorded, id)
	}
	o := l.owned[id]
	if o == nil {
		return nil, errReadOnly
	}
	if l.foreign(o) {
		return nil, l.foreignError(*o)
	}
	mcp := l.opts.mcpConfig()
	cmd := exec.Command(l.opts.CLI, l.launchArgs(id, *o, model, effort, mcp)...)
	cmd.Dir = o.Directory
	cmd.Env = claudecli.Environment(l.opts.ConfigEnv, o.Directory)
	cmd.Env = append(cmd.Env, "OCMAN_CLAUDE_MANAGED=1")
	p := &proc{id: id, model: model, effort: effort, mcp: mcp, cmd: cmd, busy: true, pending: map[string]bool{}, conv: &Converter{SessionID: id},
		children: map[string]*Converter{}, tasks: map[string]bool{}, controls: map[string]chan controlResult{}, dir: o.Directory, stderr: &tail{}, done: make(chan struct{})}
	cmd.Stderr = p.stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting claude: %w", err)
	}
	p.stdin = stdin
	l.procs[id] = p
	l.children++
	go l.read(p, stdout)
	return p, nil
}

// stopLocked closes stdin, which ends a claude child between turns; a child
// that ignores it is killed.
func (l *live) stopLocked(p *proc) {
	if l.procs[p.id] == p {
		delete(l.procs, p.id)
	}
	// The slot stays taken until read() reaps the child: the cap counts
	// live processes, not sessions (NFR-2).
	if p.idle != nil {
		p.idle.Stop()
	}
	l.stopRunCeilingLocked(p)
	_ = p.stdin.Close()
	time.AfterFunc(5*time.Second, func() {
		select {
		case <-p.done:
		default:
			_ = p.cmd.Process.Signal(syscall.SIGTERM)
			time.AfterFunc(5*time.Second, func() {
				select {
				case <-p.done:
				default:
					_ = p.cmd.Process.Kill()
				}
			})
		}
	})
}

// stopAll ends the live: its children go and the retry workers retire
// (idempotent: both the provider shutdown and the test harness call it).
func (l *live) stopAll() {
	l.stopOnce.Do(func() {
		close(l.done)
		if l.stop != nil {
			l.stop()
		}
	})
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, p := range l.procs {
		l.stopLocked(p)
	}
}

func (l *live) write(p *proc, msg any) error {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	return l.writeLocked(p, msg)
}

// writeLocked writes one stdin frame; the caller holds p.writeMu, so the
// frame's position relative to other frames is the caller's decision.
func (l *live) writeLocked(p *proc, msg any) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	_, err = p.stdin.Write(append(data, '\n'))
	return err
}

// handle converts one stream-json line into v1 events.
func (l *live) handle(p *proc, raw []byte) error {
	var m struct {
		Type   string `json:"type"`
		Parent string `json:"parent_tool_use_id"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return fmt.Errorf("invalid Claude stream JSON: %w", err)
	}
	if m.Type == "" {
		return errors.New("claude stream message has no type")
	}
	switch m.Type {
	case "control_response":
		l.receiveControl(p, raw)
	case "control_request":
		l.ask(p, raw)
	case "control_cancel_request":
		var c struct {
			RequestID string `json:"request_id"`
		}
		_ = json.Unmarshal(raw, &c)
		l.cancel(p, c.RequestID)
	case "system":
		l.system(p, raw)
	case "assistant", "user", "stream_event":
		if m.Parent == "" && (m.Type == "assistant" || m.Type == "stream_event") {
			l.resumeRun(p)
		}
		// Forwarded subagent output names the spawning call; it renders in
		// the child session linked to that call, never in the parent (FR-3).
		conv := p.conv
		if m.Type == "assistant" && m.Parent == "" {
			l.mu.Lock()
			p.replied = true
			l.mu.Unlock()
		}
		if m.Parent != "" {
			if conv = p.children[m.Parent]; conv == nil {
				return nil // ponytail: no task_started link yet; the child's transcript renders it
			}
		}
		var echo struct {
			IsReplay bool   `json:"isReplay"`
			UUID     string `json:"uuid"`
		}
		if m.Type == "user" && m.Parent == "" && json.Unmarshal(raw, &echo) == nil && echo.IsReplay {
			l.started(p, echo.UUID)
		}
		open, _ := conv.Live()
		var info map[string]any
		var parts []map[string]any
		if m.Type == "stream_event" {
			var part map[string]any
			if info, part = conv.Stream(raw); part != nil {
				parts = append(parts, part)
			}
		} else {
			conv.Add(raw)
			info, parts = conv.Live()
		}
		conv.Messages, conv.Parts = nil, nil // finished groups are already published
		if cur, _ := conv.Live(); open != nil && (cur == nil || cur["id"] != open["id"]) {
			l.publish(conv.SessionID, open, nil) // closed: now with its completion
		}
		l.publish(conv.SessionID, openInfo(info), parts)
	case "rate_limit_event":
		var e struct {
			Info json.RawMessage `json:"rate_limit_info"`
		}
		if json.Unmarshal(raw, &e) == nil {
			l.account.observe(e.Info)
		}
	case "result":
		l.closeOpen(p)
		if !l.initializationFailure(p, raw) {
			l.result(p, raw)
		}
	}
	return nil
}

// exitingLocked reports a stopped child that has not been reaped yet. While
// one exits no further child is evicted: its slot frees on exit.
func (l *live) exitingLocked() bool {
	return l.children > len(l.procs)
}
