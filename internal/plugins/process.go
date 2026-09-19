package plugins

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"
)

const (
	MaxStderrBytes     = 64 << 10
	MaxCallOutputBytes = 8 << 20
	maxBufferedFrames  = 16
)

var (
	ErrUnavailable = errors.New("plugin unavailable")
	ErrBusy        = errors.New("plugin concurrency limit reached")
	ErrOutputLimit = errors.New("plugin output limit exceeded")
)

// Health contains only host-generated diagnostics, never stderr or transport errors.
type Health struct {
	Status       string
	RestartCount int
	LastError    string
}

type LaunchConfig struct {
	Candidate Discovery
	DataDir   string
	Supported []Capability
	OnHealth  func(Health)
	// Configuration is JSON delivered on inherited fd 3, never argv or env.
	Configuration json.RawMessage
}

// Reply is either a chunk, a terminal result, or a terminal local error.
// The caller must drain replies promptly. Slow consumers cannot grow host memory.
type Reply struct {
	Message Envelope
	Err     error
}

type invocation struct {
	ctx       context.Context
	call      Call
	replies   chan Reply
	bytes     int
	cancelled time.Time
}

type processPolicy struct {
	ready, grace, backoff, maxBackoff time.Duration
	restarts                          int
}

var defaultProcessPolicy = processPolicy{3 * time.Second, time.Second, 100 * time.Millisecond, 5 * time.Second, 5}

// Process owns one plugin's lifecycle. Its owner must serialize enable/disable
// and keep only one Process per plugin ID. Failures never replay calls.
type Process struct {
	config LaunchConfig
	policy processPolicy
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	calls  chan *invocation
	events chan Event
	mu     sync.Mutex
	health Health
	stderr []byte // Sensitive, bounded, memory-only; never exported or logged.
	tokens []string
}

func StartProcess(ctx context.Context, config LaunchConfig) (*Process, error) {
	return startProcess(ctx, config, defaultProcessPolicy)
}

func startProcess(ctx context.Context, config LaunchConfig, policy processPolicy) (*Process, error) {
	d := config.Candidate
	info, err := os.Lstat(config.DataDir)
	if d.Err != nil || d.Description.Validate() != nil || !filepath.IsAbs(d.Path) ||
		!filepath.IsAbs(config.DataDir) || err != nil || !info.IsDir() || info.Mode().Perm() != 0700 ||
		validateCapabilities(config.Supported) != nil {
		return nil, ErrInvalidMessage
	}
	// Copy all declaration slices so callers cannot change approval mid-launch.
	data, _ := json.Marshal(d.Description)
	config.Candidate.Description = Description{}
	_ = json.Unmarshal(data, &config.Candidate.Description)
	config.Supported = append([]Capability(nil), config.Supported...)
	if len(config.Configuration) == 0 {
		config.Configuration = json.RawMessage(`{}`)
	}
	if len(config.Configuration) > MaxMessageBytes || !json.Valid(config.Configuration) {
		return nil, ErrInvalidMessage
	}
	config.Configuration = append(json.RawMessage(nil), config.Configuration...)
	ctx, cancel := context.WithCancel(ctx)
	p := &Process{config: config, policy: policy, ctx: ctx, cancel: cancel,
		done: make(chan struct{}), calls: make(chan *invocation), events: make(chan Event, maxBufferedFrames)}
	go p.supervise()
	return p, nil
}

func (p *Process) Health() Health {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.health
}

// RedactedStderr exposes bounded local diagnostics only through the owner's
// secret redactor. Suppress truncated captures rather than leak partial secrets.
func (p *Process) RedactedStderr(redact func(string) string) string {
	p.mu.Lock()
	text := string(p.stderr)
	full := len(p.stderr) == MaxStderrBytes
	tokens := append([]string(nil), p.tokens...)
	p.mu.Unlock()
	if full {
		return "[stderr limit reached]"
	}
	for _, token := range tokens {
		text = strings.ReplaceAll(text, token, "[REDACTED]")
		for n := len(token) - 1; n > 0; n-- {
			if strings.HasSuffix(text, token[:n]) {
				text = strings.TrimSuffix(text, token[:n]) + "[REDACTED]"
				break
			}
		}
	}
	text = redact(text)
	return text[:min(len(text), MaxStderrBytes)]
}

func (p *Process) setHealth(status string, restarts int, err error) {
	h := Health{Status: status, RestartCount: restarts}
	if err != nil {
		h.LastError = err.Error()
	}
	p.mu.Lock()
	p.health = h
	p.mu.Unlock()
	if p.config.OnHealth != nil {
		p.config.OnHealth(h)
	}
}

// Events is bounded like call output. An unconsumed event flood fails the process.
func (p *Process) Events() <-chan Event { return p.events }

// Call assigns the request ID. OperationID is preserved for caller-controlled
// deduplication. Both the wire deadline and context are enforced, including while
// cancelling; a slot remains occupied until a result or process termination.
func (p *Process) Call(ctx context.Context, call Call) (<-chan Reply, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	call.ID = 1 // Validate before consuming a process request ID.
	if (Envelope{Type: TypeCall, Call: &call}).Validate() != nil {
		return nil, ErrInvalidMessage
	}
	// Reserve the full uint64 width before the actual ID is assigned.
	call.ID = ^uint64(0)
	data, err := json.Marshal(Envelope{Type: TypeCall, Call: &call})
	if err != nil {
		return nil, ErrInvalidMessage
	}
	if len(data) > MaxMessageBytes {
		return nil, ErrMessageTooLarge
	}
	if err := checkJSON(data); err != nil {
		return nil, err
	}
	if time.Now().UnixMilli() >= call.DeadlineUnixMS {
		return nil, context.DeadlineExceeded
	}
	if p.ctx.Err() != nil || p.Health().Status != "ready" {
		return nil, ErrUnavailable
	}
	call.Params = append(json.RawMessage(nil), call.Params...)
	replies := make(chan Reply, maxBufferedFrames+1)
	r := &invocation{ctx: ctx, call: call, replies: replies}
	select {
	case p.calls <- r:
		return replies, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-p.done:
		return nil, ErrUnavailable
	case <-p.ctx.Done():
		return nil, ErrUnavailable
	}
}

// Stop waits until the child and its process group have been reaped. It is safe
// to repeat, and prevents restarts even if shutdown races a crash.
func (p *Process) Stop() { p.cancel(); <-p.done }

func (p *Process) supervise() {
	defer close(p.done)
	defer close(p.events)
	delay := p.policy.backoff
	for attempt := 0; ; attempt++ {
		if p.ctx.Err() != nil {
			p.setHealth("stopped", attempt, nil)
			return
		}
		p.setHealth("starting", attempt, nil)
		err := p.serve(attempt)
		if p.ctx.Err() != nil {
			p.setHealth("stopped", attempt, nil)
			return
		}
		if attempt == p.policy.restarts {
			p.setHealth("unhealthy", attempt, err)
			return
		}
		p.setHealth("starting", attempt, err)
		timer := time.NewTimer(delay)
		select {
		case <-timer.C:
		case <-p.ctx.Done():
			timer.Stop()
		}
		delay = min(delay*2, p.policy.maxBackoff)
	}
}

func (p *Process) launchToken() string {
	var random [32]byte
	_, _ = rand.Read(random[:])
	token := hex.EncodeToString(random[:])
	p.mu.Lock()
	p.tokens = append(p.tokens, token)
	p.mu.Unlock()
	return token
}

func (p *Process) approvedExecutable() error {
	d := p.config.Candidate
	info, err := os.Lstat(d.Path)
	if err != nil {
		return ErrExecutableChanged
	}
	sum, err := executableChecksum(d.Path, info)
	if err != nil || sum != d.Checksum {
		return ErrExecutableChanged
	}
	return nil
}

func (p *Process) matchesOffer(e Envelope) bool {
	return e.Hello != nil && e.Hello.Description != nil && reflect.DeepEqual(*e.Hello.Description, p.config.Candidate.Description)
}

func finish(r *invocation, reply Reply) {
	if r.replies == nil {
		return
	}
	// One reserved terminal slot means even an abandoned stream is settled.
	r.replies <- reply
	close(r.replies)
	r.replies = nil
}

func invocationError(r *invocation, now time.Time) error {
	if err := r.ctx.Err(); err != nil {
		return err
	}
	if now.UnixMilli() >= r.call.DeadlineUnixMS {
		return context.DeadlineExceeded
	}
	return nil
}

// stderrCapture retains a prefix rather than a tail, avoiding partial-secret
// suffixes from ring-buffer truncation. Management suppresses a full capture.
type stderrCapture struct{ p *Process }

func (w stderrCapture) Write(b []byte) (int, error) {
	w.p.mu.Lock()
	defer w.p.mu.Unlock()
	n := min(len(b), MaxStderrBytes-len(w.p.stderr))
	w.p.stderr = append(w.p.stderr, b[:n]...)
	return len(b), nil
}
