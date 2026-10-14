package claudeprovider

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
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
	children map[string]*Converter
	dir      string // working directory
	stderr   *tail
	done     chan struct{} // closed once the child has been reaped
	release  func()        // frees the machine slot once the child is reaped; under l.mu
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

// launchArgs is the AD-6 command. --verbose is required by stream-json
// output and --forward-subagent-text streams child transcripts (spike).
func (l *live) launchArgs(id string, o owned, model, effort, mcp string) []string {
	args := []string{"-p", "--verbose", "--input-format", "stream-json", "--output-format", "stream-json",
		"--include-partial-messages", "--replay-user-messages", "--forward-subagent-text",
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
type waitTicket struct{ cancelled bool }

// acquire returns the session's child marked busy, spawning (or resuming) it.
// At the cap an idle child is evicted; when every child is busy it waits.
// input is recorded as pending when it joins an open turn.
func (l *live) acquire(id, model, effort, input string, w *waitTicket) (*proc, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var waiter *os.File
	defer func() { dropWaiter(waiter) }()
	for {
		if w.cancelled {
			return nil, errAborted
		}
		if p := l.procs[id]; p != nil {
			// ponytail: model/effort switches restart an idle child; a busy one keeps its settings.
			// So does a rebound MCP listener: a running turn is never interrupted.
			if p.busy || ((model == "" || model == p.model) && effort == p.effort && p.mcp == l.opts.mcpConfig()) {
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
			l.waitSlotLocked(&waiter) // the evicted child frees its slot when reaped
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
	cmd.Env = l.opts.childEnv()
	p := &proc{id: id, model: model, effort: effort, mcp: mcp, cmd: cmd, busy: true, pending: map[string]bool{}, conv: &Converter{SessionID: id},
		children: map[string]*Converter{}, dir: o.Directory, stderr: &tail{}, done: make(chan struct{})}
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
	_ = p.stdin.Close()
	time.AfterFunc(5*time.Second, func() {
		select {
		case <-p.done:
		default:
			_ = p.cmd.Process.Kill()
		}
	})
}

func (l *live) stopAll() {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, p := range l.procs {
		l.stopLocked(p)
	}
}

func (l *live) write(p *proc, msg any) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	_, err = p.stdin.Write(append(data, '\n'))
	return err
}

func (l *live) read(p *proc, stdout io.Reader) {
	lines := bufio.NewScanner(stdout)
	lines.Buffer(nil, 32<<20)
	for lines.Scan() {
		l.handle(p, lines.Bytes())
	}
	_ = p.cmd.Wait()
	close(p.done)
	l.cancel(p, "")
	l.mu.Lock()
	wasBusy := p.busy
	p.busy = false
	clear(p.pending)
	if l.procs[p.id] == p {
		delete(l.procs, p.id)
	}
	p.freeSlotLocked()
	l.children--
	gen := l.accepted[p.id]
	l.slots.Broadcast()
	l.mu.Unlock()
	if wasBusy {
		l.fail(p.id, fmt.Errorf("claude exited during the turn: %s", or(p.stderr.String(), p.cmd.ProcessState.String())), gen)
	}
}

// handle converts one stream-json line into v1 events.
func (l *live) handle(p *proc, raw []byte) {
	var m struct {
		Type   string `json:"type"`
		Parent string `json:"parent_tool_use_id"`
	}
	if json.Unmarshal(raw, &m) != nil {
		return
	}
	switch m.Type {
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
				return // ponytail: no task_started link yet; the child's transcript renders it
			}
		}
		var echo struct {
			IsReplay bool   `json:"isReplay"`
			UUID     string `json:"uuid"`
		}
		if m.Type == "user" && m.Parent == "" && json.Unmarshal(raw, &echo) == nil && echo.IsReplay {
			l.started(p, echo.UUID)
		}
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
		l.publish(conv.SessionID, info, parts)
	case "rate_limit_event":
		var e struct {
			Info json.RawMessage `json:"rate_limit_info"`
		}
		if json.Unmarshal(raw, &e) == nil {
			l.account.observe(e.Info)
		}
	case "result":
		l.result(p, raw)
	}
}

// exitingLocked reports a stopped child that has not been reaped yet. While
// one exits no further child is evicted: its slot frees on exit.
func (l *live) exitingLocked() bool {
	return l.children > len(l.procs)
}
