package claudeprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/claudeprovider/claudetest"
	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/provider"
)

type lifecycle struct {
	mu     sync.Mutex
	status map[string]db.SessionStatus
	resets map[string]int64
	idles  map[string]int
}

func (c *lifecycle) get(id string) (db.SessionStatus, int64, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status[id], c.resets[id], c.idles[id]
}

type liveHarness struct {
	adapter *provider.Adapter
	live    *live
	events  *lifecycle
	args    string
	dir     string
}

// startLive serves the provider with the fake claude replaying fixture and
// starts the adapter's headless event stream.
func startLive(t *testing.T, fixturePath string, tweak func(*Options)) *liveHarness {
	t.Helper()
	h := serveLive(t, fixturePath, tweak)
	go func() { _ = h.adapter.WatchEvents(t.Context()) }()
	return h
}

// serveLive is startLive without the headless stream.
func serveLive(t *testing.T, fixturePath string, tweak func(*Options)) *liveHarness {
	t.Helper()
	binary := claudetest.Binary(t)
	dir, err := os.MkdirTemp("", "cl-") // short: unix socket path limit
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	h := &liveHarness{args: filepath.Join(dir, "args.json"), dir: dir}
	t.Setenv("FAKE_CLAUDE_FIXTURE", fixturePath)
	t.Setenv("FAKE_CLAUDE_ARGS", h.args)
	opts := Options{IndexPath: filepath.Join(dir, "claude-index.db"), ConfigDir: fixture(t), CLI: binary,
		Sweep: time.Hour, StatePath: filepath.Join(dir, "owned.json"), MCPConfig: `{"mcpServers":{}}`, PluginDir: dir}
	if tweak != nil {
		tweak(&opts)
	}
	sup, l, err := newSupervisor(t.Context(), opts, filepath.Join(dir, "spool"))
	if err != nil {
		t.Fatal(err)
	}
	h.live = l
	socket := filepath.Join(dir, "s")
	go func() { _ = sup.Serve(t.Context(), socket) }()
	for deadline := time.Now().Add(5 * time.Second); h.adapter == nil; time.Sleep(10 * time.Millisecond) {
		if h.adapter, err = provider.New(t.Context(), socket); err != nil && time.Now().After(deadline) {
			t.Fatal(err)
		}
	}
	t.Cleanup(h.adapter.Close)
	h.events = &lifecycle{status: map[string]db.SessionStatus{}, resets: map[string]int64{}, idles: map[string]int{}}
	h.adapter.SetSessionEventSink(platforms.SessionEventSink{
		OnSessionStatus: func(_, id string, s db.SessionStatus, resetsAt int64) {
			h.events.mu.Lock()
			h.events.status[id], h.events.resets[id] = s, resetsAt
			h.events.mu.Unlock()
		},
		OnSessionIdle: func(_, id string) {
			h.events.mu.Lock()
			h.events.idles[id]++
			h.events.mu.Unlock()
		},
	})
	t.Cleanup(l.stopAll)
	return h
}

func (h *liveHarness) create(t *testing.T) string {
	t.Helper()
	res, err := h.adapter.CreateSession(t.Context(), platforms.CreateSessionRequest{Directory: h.dir, Title: "fresh"})
	if err != nil {
		t.Fatal(err)
	}
	return res.ID
}

func (h *liveHarness) launchArgs(t *testing.T) []string {
	t.Helper()
	var args []string
	data, err := os.ReadFile(h.args)
	if err == nil {
		err = json.Unmarshal(data, &args)
	}
	if err != nil {
		t.Fatal(err)
	}
	return args
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); !ok(); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func (h *liveHarness) procs() int {
	h.live.mu.Lock()
	defer h.live.mu.Unlock()
	return len(h.live.procs)
}

// An idle child is stopped after the idle timeout and the next send resumes
// the same claude session.
func TestIdleTimeoutResumesSession(t *testing.T) {
	h := startLive(t, claudetest.Fixture("plain.jsonl"), func(o *Options) { o.IdleTimeout = 100 * time.Millisecond })
	id := h.create(t)
	if sessions, err := h.adapter.Sessions(t.Context(), h.dir, 0); err != nil || len(sessions) != 1 || sessions[0].ID != id || sessions[0].Title != "fresh" {
		t.Fatalf("new session must list before its transcript exists: %+v %v", sessions, err)
	}
	if err := h.adapter.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: id, Message: "hi", Model: "anthropic/haiku"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "first turn idle", func() bool { _, _, idles := h.events.get(id); return idles == 1 })
	args := h.launchArgs(t)
	for _, want := range []string{"--verbose", "--replay-user-messages", "--include-partial-messages", "--mcp-config", "--plugin-dir"} {
		if !slices.Contains(args, want) {
			t.Fatalf("launch args %q miss %s", args, want)
		}
	}
	if slices.Contains(args, "--bare") || !slices.Contains(args, "--session-id") || args[slices.Index(args, "--session-id")+1] != id ||
		args[slices.Index(args, "--model")+1] != "haiku" {
		t.Fatalf("first launch args = %q", args)
	}
	waitFor(t, "idle child stopped", func() bool { return h.procs() == 0 })

	if err := h.adapter.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: id, Message: "again"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "resumed turn idle", func() bool { _, _, idles := h.events.get(id); return idles == 2 })
	if args := h.launchArgs(t); !slices.Contains(args, "--resume") || args[slices.Index(args, "--resume")+1] != id || slices.Contains(args, "--session-id") {
		t.Fatalf("resume args = %q", args)
	}

	// The browser stream carries the converted reply.
	var buf bytes.Buffer
	var mu sync.Mutex
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() { _ = h.adapter.ProxyEvents(ctx, id, lockedWriter{&mu, &buf}, func() {}) }()
	waitFor(t, "streamed reply", func() bool { mu.Lock(); defer mu.Unlock(); return strings.Contains(buf.String(), "SPIKE_OK") })

	// Ownership survives a restart of the provider.
	if !strings.Contains(string(must(os.ReadFile(filepath.Join(h.dir, "owned.json")))), id) {
		t.Fatal("owned sessions are not persisted")
	}
}

func TestAbortInterruptsTurn(t *testing.T) {
	fixturePath := filepath.Join(t.TempDir(), "abort.jsonl")
	rows := `{"direction":"stdin","message":{"type":"user"}}
{"direction":"stdout","message":{"type":"assistant","message":{"id":"msg_a","model":"haiku","content":[{"type":"text","text":"working"}]}}}
{"direction":"stdin","message":{"type":"control_request","request":{"subtype":"interrupt"}}}
{"direction":"stdout","message":{"type":"result","is_error":true,"subtype":"error_during_execution"}}
`
	if err := os.WriteFile(fixturePath, []byte(rows), 0o600); err != nil {
		t.Fatal(err)
	}
	stdin := filepath.Join(t.TempDir(), "stdin.jsonl")
	t.Setenv("FAKE_CLAUDE_STDIN", stdin)
	h := startLive(t, fixturePath, nil)
	id := h.create(t)
	if err := h.adapter.Abort(t.Context(), platforms.AbortRequest{SessionID: id}); err != nil {
		t.Fatalf("abort without a turn: %v", err)
	}
	if err := h.adapter.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: id, Message: "hi"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "streamed part", func() bool {
		for _, e := range h.live.snapshot(id) {
			if strings.Contains(string(e.Properties), "working") {
				return true
			}
		}
		return false
	})
	if err := h.adapter.Abort(t.Context(), platforms.AbortRequest{SessionID: id}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "aborted turn idle", func() bool { _, _, idles := h.events.get(id); return idles == 1 })
	settledAs(t, h.adapter, id, db.StatusDone) // a user abort is not a failure
	// The fixture only accepts an interrupt (the fake matches the recorded
	// subtype); the CLI received exactly one.
	if n := strings.Count(string(must(os.ReadFile(stdin))), `"subtype":"interrupt"`); n != 1 {
		t.Fatalf("interrupts sent = %d, want 1", n)
	}
}

// Fork resumes the source with --fork-session on its first send; rename and
// commands are turns or transcript records.
func TestForkRenameAndCommands(t *testing.T) {
	h := startLive(t, claudetest.Fixture("plain.jsonl"), nil)
	src := h.create(t)
	if err := h.adapter.Compact(t.Context(), platforms.CompactRequest{SessionID: src}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "source turn idle", func() bool { _, _, idles := h.events.get(src); return idles == 1 })
	fork, err := h.adapter.ForkSession(t.Context(), platforms.ForkSessionRequest{SessionID: src})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.adapter.ExecuteCommand(t.Context(), platforms.ExecuteCommandRequest{SessionID: fork.ID, Command: "review", Arguments: "now"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "fork turn idle", func() bool { _, _, idles := h.events.get(fork.ID); return idles == 1 })
	args := h.launchArgs(t)
	if args[slices.Index(args, "--resume")+1] != src || !slices.Contains(args, "--fork-session") || args[slices.Index(args, "--session-id")+1] != fork.ID {
		t.Fatalf("fork args = %q", args)
	}
	if o, _ := h.live.ownedSession(fork.ID); !o.Started || o.ForkOf != "" || o.Directory != h.dir {
		t.Fatalf("fork state after first turn = %+v", o)
	}
	// Only whole-session forks are measured; fork-at-message is gated off.
	if caps := h.adapter.Capabilities(); !caps.Fork || caps.ForkAtMessage || caps.ShellExec || caps.Revert {
		t.Fatalf("capabilities = %+v", caps)
	}
	if _, err := h.adapter.ForkSession(t.Context(), platforms.ForkSessionRequest{SessionID: src, MessageID: "m"}); !errors.Is(err, platforms.ErrUnsupported) {
		t.Fatalf("fork at message = %v", err)
	}

	if err := h.adapter.RenameSession(t.Context(), platforms.RenameSessionRequest{SessionID: fork.ID, Title: "  Renamed  "}); err != nil {
		t.Fatal(err)
	}
	if o, _ := h.live.ownedSession(fork.ID); o.Title != "Renamed" {
		t.Fatalf("title = %q", o.Title)
	}
	if err := h.adapter.RenameSession(t.Context(), platforms.RenameSessionRequest{SessionID: fork.ID}); err == nil {
		t.Fatal("empty title must be rejected")
	}
	// A transcript-backed owned session gets Claude's custom-title record.
	path := filepath.Join(h.live.opts.ConfigDir, "projects", "-work-repo", mainID+".jsonl")
	h.live.mu.Lock()
	if err := h.live.updateOwnedLocked(func() { h.live.owned[mainID] = &owned{Directory: "/work/repo", Started: true} }); err != nil {
		t.Fatal(err)
	}
	h.live.mu.Unlock()
	if err := h.adapter.RenameSession(t.Context(), platforms.RenameSessionRequest{SessionID: mainID, Title: "Mine"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(must(os.ReadFile(path))), `"customTitle":"Mine"`) {
		t.Fatal("rename did not append the custom-title record")
	}

	pending, err := h.adapter.CreateSession(t.Context(), platforms.CreateSessionRequest{Directory: h.dir})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.adapter.ForkSession(t.Context(), platforms.ForkSessionRequest{SessionID: pending.ID}); err == nil {
		t.Fatal("forking a session without messages must fail")
	}
	detail, err := h.adapter.Session(t.Context(), pending.ID, 10, 0)
	if err != nil || detail.Session.ID != pending.ID || len(detail.Messages) != 0 {
		t.Fatalf("unindexed detail = %+v %v", detail, err)
	}
	catalog, err := h.adapter.DirectoryCatalog(t.Context(), platforms.DirectoryCatalogRequest{Directory: h.dir})
	if err != nil || len(catalog.Models.Models) != 3 {
		t.Fatalf("catalog = %+v %v", catalog, err)
	}
}

// Outside the pinned range every create path fails with an explicit error.
func TestUnsupportedCLIVersion(t *testing.T) {
	t.Setenv("FAKE_CLAUDE_VERSION", "2.0.9")
	h := startLive(t, claudetest.Fixture("plain.jsonl"), nil)
	_, err := h.adapter.CreateSession(t.Context(), platforms.CreateSessionRequest{Directory: h.dir})
	if err == nil || !strings.Contains(err.Error(), "claude CLI 2.0.9 is unsupported") {
		t.Fatalf("create error = %v", err)
	}
	for v, ok := range map[string]bool{"2.1.220": true, "2.1.289": false, "2.1.219": false, "2.2.0": false, "unavailable": false} {
		if got := checkCLI(v) == nil; got != ok {
			t.Fatalf("checkCLI(%q) ok = %v", v, got)
		}
	}
	// Every accepted version has recorded fixtures (NFR-5 evidence).
	for _, v := range TestedCLI {
		for _, name := range []string{"plain.jsonl", "permission.jsonl", "subagent.jsonl"} {
			if _, err := os.Stat(filepath.Join(filepath.Dir(claudetest.Fixture(name)), "..", v, name)); err != nil {
				t.Fatalf("accepted CLI %s has no %s fixture: %v", v, name, err)
			}
		}
	}
}

func TestUserContent(t *testing.T) {
	got := userContent("look", []platforms.ImageAttachment{{URL: "data:image/png;base64,AAA="}, {URL: "https://x/y.png"}})
	if len(got) != 2 || got[0]["type"] != "image" || got[0]["source"].(map[string]any)["media_type"] != "image/png" || got[1]["text"] != "look" {
		t.Fatalf("content = %+v", got)
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

type lockedWriter struct {
	mu *sync.Mutex
	w  io.Writer
}

func (l lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// At the cap an idle child is evicted; a launch failure ends the turn with a
// visible error message.
func TestPoolCapAndLaunchFailure(t *testing.T) {
	h := startLive(t, claudetest.Fixture("plain.jsonl"), func(o *Options) { o.MaxProcs = 1 })
	a, b := h.create(t), h.create(t)
	for i, id := range []string{a, b} {
		if err := h.adapter.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: id, Message: "hi"}); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "turn idle", func() bool { _, _, idles := h.events.get(id); return idles == 1 })
		h.live.mu.Lock()
		_, live := h.live.procs[id]
		n := len(h.live.procs)
		h.live.mu.Unlock()
		if !live || n != 1 {
			t.Fatalf("turn %d: live=%v procs=%d", i, live, n)
		}
	}

	h.live.mu.Lock()
	err := h.live.updateOwnedLocked(func() { h.live.owned["gone"] = &owned{Directory: filepath.Join(h.dir, "missing")} })
	h.live.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err := h.adapter.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: "gone", Message: "hi"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "failed turn idle", func() bool { _, _, idles := h.events.get("gone"); return idles == 1 })
	settledAs(t, h.adapter, "gone", db.StatusError)
	found := false
	for _, e := range h.live.snapshot("gone") {
		found = found || strings.Contains(string(e.Properties), "starting claude")
	}
	if !found {
		t.Fatalf("launch failure not visible: %+v", h.live.snapshot("gone"))
	}
}

func TestPluginAndMCPConfig(t *testing.T) {
	dir, err := WritePlugin(t.TempDir(), map[string][]byte{"ocman-x": []byte("skill")}, "")
	if err != nil {
		t.Fatal(err)
	}
	if string(must(os.ReadFile(filepath.Join(dir, "skills", "ocman-x", "SKILL.md")))) != "skill" {
		t.Fatal("skill not written")
	}
	if _, err := os.Stat(filepath.Join(dir, ".claude-plugin", "plugin.json")); err != nil {
		t.Fatal(err)
	}
	if MCPConfig("") != "" || !strings.Contains(MCPConfig("127.0.0.1:8227"), "http://127.0.0.1:8227/mcp") {
		t.Fatal("mcp config")
	}
}
