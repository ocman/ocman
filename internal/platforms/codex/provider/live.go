package codexprovider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	codexappserver "github.com/NoUseFreak/ocman/internal/platforms/codex/appserver"
	"github.com/NoUseFreak/ocman/internal/providerprotocol"
	"github.com/NoUseFreak/ocman/internal/providersupervisor"
)

// Options configures a Codex supervisor.
type Options struct {
	CLI     string   // codex executable
	Args    []string // extra app-server arguments, such as -c overrides
	Env     []string // child environment; nil inherits this process's
	Dir     string   // app-server working directory, the project root
	Version string   // ocman version reported as clientInfo
	// Start connects to an app-server. Nil spawns CLI; tests attach a fake.
	Start func(context.Context, codexappserver.Handler) (*codexappserver.Client, error)
}

// Capabilities are what a Codex supervisor offers. Rulesets live in ocman
// (provider.Rules), never in the supervisor, which answers the rules routes
// with 501. Revert is not offered: thread/revert drops history without
// restoring files and has no redo. Codex has no switchable main agents.
func Capabilities() providerprotocol.Capabilities {
	return providerprotocol.Capabilities{Rename: true, Ownership: true, Capabilities: platforms.Capabilities{
		Composer: true, Abort: true, Events: true, RespondPermission: true, RespondQuestion: true,
		AutoApprove: true, PushPrompts: true, ExactPatterns: true, PermissionRules: true, ModelCatalog: true,
		Compact: true, Fork: true, ForkAtMessage: true, ShellExec: true, SlashCommands: true, FileChanges: true, SessionInfo: true}}
}

// NewSupervisor serves Codex threads over the provider protocol. One
// app-server process hosts every thread the supervisor drives; it starts on
// first use and again after it exits.
func NewSupervisor(ctx context.Context, opts Options, spool string) (*providersupervisor.Supervisor, error) {
	sup, _, err := newSupervisor(ctx, opts, spool)
	return sup, err
}

func newSupervisor(ctx context.Context, opts Options, spool string) (*providersupervisor.Supervisor, *backend, error) {
	b := &backend{opts: opts, ctx: ctx, sessions: map[string]*session{}, prompts: map[string]*prompt{}}
	sup, err := providersupervisor.New(spool, providersupervisor.Backend{
		Hello: providerprotocol.Hello{ProtocolVersion: providerprotocol.CurrentVersion, PlatformID: PlatformID, CLIVersion: cliVersion(opts),
			Capabilities: Capabilities()},
		Handler:  b,
		Snapshot: func(_ context.Context, id string) ([]providerprotocol.Event, error) { return b.snapshot(id), nil },
	})
	if err != nil {
		return nil, nil, err
	}
	b.sup = sup
	go b.recoverPublications()
	go func() {
		<-ctx.Done()
		b.closeClient()
	}()
	return sup, b, nil
}

// backend is the Codex half of the supervisor. Lock order: the supervisor's
// publishing lock, then mu; nothing holding mu calls into the supervisor or
// the app-server.
type backend struct {
	opts Options
	ctx  context.Context
	sup  *providersupervisor.Supervisor

	startMu       sync.Mutex // serializes app-server starts
	publicationMu sync.Mutex // serializes pending upserts and terminal recovery
	pending       map[string][]viewEntry
	ends          map[string]turnEnd

	mu       sync.Mutex
	client   *codexappserver.Client
	gen      uint64 // client generation; events of older clients are dropped
	loaded   map[string]bool
	sessions map[string]*session
	prompts  map[string]*prompt
}

// session is one thread this supervisor has driven or observed.
type session struct {
	sendMu          sync.Mutex
	loadMu          sync.Mutex // all callers share one resume per connection
	provider, model string
	dir             string
	title           string
	accepted        uint64 // turns admitted (BeginTurn)
	running         bool   // admitted and not settled
	turn            string // the running Codex turn
	conv            *Converter
	base            *codexappserver.TokenUsage // thread totals before the turn
	used            *TurnUsage                 // the turn's usage so far
	status          db.SessionStatus
	view            []viewEntry
}

type viewEntry struct {
	key   string
	event providerprotocol.Event
}

func cliVersion(opts Options) string {
	if opts.CLI == "" {
		return "unavailable"
	}
	return codexVersion(opts.CLI)
}

// conn returns the running app-server, starting one when none is.
func (b *backend) conn(ctx context.Context) (*codexappserver.Client, uint64, error) {
	b.startMu.Lock()
	defer b.startMu.Unlock()
	b.mu.Lock()
	c, gen := b.client, b.gen
	b.mu.Unlock()
	if c != nil {
		select {
		case <-c.Done():
			b.retire(gen, c.Err())
			_ = c.Close()
		default:
			return c, gen, nil
		}
	}
	b.mu.Lock()
	b.gen++
	gen = b.gen
	b.mu.Unlock()
	h := &events{b: b, gen: gen}
	start := b.opts.Start
	if start == nil {
		start = b.spawn
	}
	c, err := start(ctx, h)
	if err != nil {
		return nil, 0, fmt.Errorf("starting codex app-server: %w", err)
	}
	b.mu.Lock()
	b.client, b.loaded = c, map[string]bool{}
	b.mu.Unlock()
	go func() {
		<-c.Done()
		b.lost(gen, c.Err())
		_ = c.Close()
	}()
	return c, gen, nil
}

func (b *backend) spawn(ctx context.Context, h codexappserver.Handler) (*codexappserver.Client, error) {
	return codexappserver.Start(ctx, codexappserver.Options{CLI: b.opts.CLI, Args: b.opts.Args, Env: b.opts.Env, Dir: b.opts.Dir,
		Client: codexappserver.ClientInfo{Name: "ocman", Title: "ocman", Version: b.opts.Version}, ExperimentalAPI: true, Stderr: os.Stderr}, h)
}

func (b *backend) closeClient() {
	b.mu.Lock()
	c := b.client
	b.mu.Unlock()
	if c != nil {
		_ = c.Close()
	}
}

// lost handles an app-server exit: its threads are no longer loaded, its
// prompts can never be answered and its running turns cannot finish.
func (b *backend) lost(gen uint64, err error) {
	b.startMu.Lock()
	defer b.startMu.Unlock()
	b.retire(gen, err)
}

func (b *backend) retire(gen uint64, err error) {
	b.mu.Lock()
	if gen != b.gen || b.client == nil {
		b.mu.Unlock()
		return
	}
	b.client, b.loaded = nil, nil
	if b.ctx.Err() != nil {
		// Shutdown: publish nothing; the turns end with this process.
		b.mu.Unlock()
		return
	}
	running := map[string]uint64{}
	for id, s := range b.sessions {
		if s.running {
			running[id] = s.accepted
		}
	}
	var stale []string
	for id := range b.prompts {
		stale = append(stale, id)
	}
	b.mu.Unlock()
	if err == nil || errors.Is(err, io.EOF) {
		err = errors.New("codex app-server exited")
	}
	for _, id := range stale {
		b.cancelPrompt(id)
	}
	for id, admission := range running {
		b.errorMessage(id, err.Error())
		b.settle(id, db.StatusError, admission)
	}
	if len(running) > 0 {
		log.WithError(err).Warn("codex: app-server exited during a turn")
	}
}

// apply runs under the supervisor's publishing lock, so snapshots and the
// event stream agree.
func (b *backend) apply(id, key string, event providerprotocol.Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.sessionLocked(id)
	switch event.Type {
	case "session.status":
		var p struct {
			Status struct {
				Type string `json:"type"`
			} `json:"status"`
		}
		_ = json.Unmarshal(event.Properties, &p)
		if p.Status.Type == string(db.StatusBusy) {
			s.view = nil
		}
		s.status = db.SessionStatus(p.Status.Type)
	case "session.idle":
		s.running, s.turn, s.conv, s.base, s.used = false, "", nil, nil, nil
		if s.status != db.StatusError && s.status != db.StatusInterrupted {
			s.status = db.StatusDone
		}
	}
	if key == "" {
		return
	}
	for i, v := range s.view {
		if v.key == key {
			s.view[i].event = event
			return
		}
	}
	s.view = append(s.view, viewEntry{key: key, event: event})
}

func (b *backend) sessionLocked(id string) *session {
	s := b.sessions[id]
	if s == nil {
		s = &session{}
		b.sessions[id] = s
	}
	return s
}

// snapshot returns the current turn's upserts and status (Backend.Snapshot).
func (b *backend) snapshot(id string) []providerprotocol.Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.sessions[id]
	if s == nil {
		return []providerprotocol.Event{}
	}
	events := make([]providerprotocol.Event, 0, len(s.view)+1)
	for _, v := range s.view {
		events = append(events, v.event)
	}
	if s.status != "" && s.status != db.StatusDone {
		props, _ := json.Marshal(map[string]any{"sessionID": id, "status": map[string]any{"type": s.status}})
		events = append(events, providerprotocol.Event{Type: "session.status", Properties: props})
	}
	if !s.running && (s.status == db.StatusDone || s.status == db.StatusError || s.status == db.StatusInterrupted) {
		props, _ := json.Marshal(map[string]any{"sessionID": id})
		events = append(events, providerprotocol.Event{Type: "session.idle", Properties: props})
	}
	return events
}

// publishMessage upserts a message and its parts.
func (b *backend) publishMessage(id string, info map[string]any, parts []map[string]any) {
	if info != nil {
		b.emit(id, "m:"+info["id"].(string), "message.updated", map[string]any{"info": info})
	}
	for _, part := range parts {
		b.emit(id, "p:"+part["id"].(string), "message.part.updated", map[string]any{"part": part})
	}
}

// errorMessage shows a turn failure that Codex reported outside an item.
func (b *backend) errorMessage(id, text string) {
	ts := now()
	info := map[string]any{"id": fmt.Sprintf("msg_err_%s_%d", id, ts), "sessionID": id, "role": "assistant",
		"time":  map[string]any{"created": ts, "completed": ts},
		"error": map[string]any{"name": "UnknownError", "data": map[string]any{"message": text}}}
	b.publishMessage(id, info, nil)
}
