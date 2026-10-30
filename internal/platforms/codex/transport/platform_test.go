package codextransport

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	codexappserver "github.com/ocman/ocman/internal/platforms/codex/appserver"
	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/state/statetest"
)

type rig struct {
	t     *testing.T
	exe   string
	data  string
	pids  string
	store *state.DB
}

func newRig(t *testing.T) *rig {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.MkdirTemp("/tmp", "cxt-") // short: the supervisor socket must fit in 103 bytes
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(data) })
	pids := installFakeTmux(t)
	t.Setenv("FAKE_CODEX", "1")
	t.Setenv("FAKE_CODEX_ACP_STATE", filepath.Join(t.TempDir(), "state.json"))
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("FAKE_CODEX_LAUNCHES", filepath.Join(data, "launches"))
	store, err := state.Open(statetest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return &rig{t: t, exe: exe, data: data, pids: pids, store: store}
}

func (r *rig) platform(ctx context.Context) *Platform {
	r.t.Helper()
	p, err := newPlatform(ctx, Config{State: r.store, DataDir: r.data, Version: "test"}, launcher{exe: r.exe, cli: r.exe, acp: r.exe})
	if err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(p.Close)
	return p
}

// killSupervisors ends every launched supervisor, as a crashed tmux session.
func (r *rig) killSupervisors() {
	r.t.Helper()
	paths, _ := filepath.Glob(filepath.Join(r.pids, "*.pid"))
	for _, path := range paths {
		data, _ := os.ReadFile(path)
		if pid, _ := strconv.Atoi(string(data)); pid > 0 {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
		_ = os.Remove(path)
	}
}

type idles struct {
	ch chan string
}

func (i *idles) sink() platforms.SessionEventSink {
	return platforms.SessionEventSink{OnSessionIdle: func(_, id string) {
		i.ch <- id
	}}
}

func (i *idles) wait(t *testing.T) string {
	t.Helper()
	select {
	case id := <-i.ch:
		return id
	case <-time.After(15 * time.Second):
		t.Fatal("no idle edge")
		return ""
	}
}

func TestSupervisorLifecycle(t *testing.T) {
	r := newRig(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := r.platform(ctx)
	if !p.Available(ctx) || p.ID() != "codex" || p.DisplayName() != "Codex" || !p.Capabilities().Composer || !p.Capabilities().PermissionRules {
		t.Fatalf("platform = %v %q %q %+v", p.Available(ctx), p.ID(), p.DisplayName(), p.Capabilities())
	}
	// Nothing is launched before a session operation needs it.
	if p.current() != nil || p.LiveStatus("thr") != nil {
		t.Fatal("supervisor attached before use")
	}
	if prompts, err := p.ListPermissions(ctx, "thr"); err != nil || prompts != nil {
		t.Fatalf("permissions before launch = %v %v", prompts, err)
	}
	if err := p.Abort(ctx, platforms.AbortRequest{SessionID: "thr"}); err != nil {
		t.Fatalf("abort before launch = %v", err)
	}
	ev := &idles{ch: make(chan string, 8)}
	p.SetSessionEventSink(ev.sink())
	go func() { _ = p.WatchEvents(ctx) }()

	res, err := p.EnsureRuntime(ctx, "/some/project")
	if err != nil || res.RepoRoot != "/some/project" || !strings.HasPrefix(res.Endpoint, "unix://") {
		t.Fatalf("EnsureRuntime = %+v %v", res, err)
	}
	if _, err := p.EnsureRuntime(ctx, "relative"); err == nil {
		t.Fatal("relative directory accepted")
	}
	created, err := p.CreateSession(ctx, platforms.CreateSessionRequest{Directory: "/repo"})
	if err != nil || created.ID != "thr" {
		t.Fatalf("CreateSession = %+v %v", created, err)
	}
	if err := p.SendMessage(ctx, platforms.SendMessageRequest{SessionID: "thr", Message: "hi"}); err != nil {
		t.Fatal(err)
	}
	if id := ev.wait(t); id != "thr" {
		t.Fatalf("idle = %q", id)
	}
	life, err := p.SessionLifecycle(ctx, "thr")
	if err != nil || life.Status != db.StatusDone {
		t.Fatalf("lifecycle = %+v %v", life, err)
	}
	if err := p.SetPermissionRules(ctx, platforms.SetPermissionRulesRequest{SessionID: "thr", Rules: []platforms.PermissionRule{{Permission: "bash", Pattern: "ls", Action: "allow"}}}); err != nil {
		t.Fatal(err)
	}
	if rules, err := p.PermissionRules(ctx, "thr"); err != nil || len(rules) != 1 {
		t.Fatalf("rules = %+v %v", rules, err)
	}
	first := p.current()

	// A supervisor that died is relaunched; the new one resumes the thread
	// and its events reach the same sink.
	r.killSupervisors()
	// A request on a pooled connection to the dead supervisor is ambiguous
	// and is not retried; it relaunches the supervisor for the next one.
	for attempt := 0; ; attempt++ {
		err := p.SendMessage(ctx, platforms.SendMessageRequest{SessionID: "thr", Message: "again"})
		if err == nil {
			break
		}
		if attempt > 0 || !errors.Is(err, platforms.ErrPlatformUnreachable) {
			t.Fatalf("send after the supervisor died = %v (attempt %d)", err, attempt)
		}
	}
	if p.current() == first {
		t.Fatal("adapter was not replaced")
	}
	if id := ev.wait(t); id != "thr" {
		t.Fatalf("idle after relaunch = %q", id)
	}
	if err := p.RenameSession(ctx, platforms.RenameSessionRequest{SessionID: "thr", Title: "T"}); err == nil {
		t.Fatal("rename without a fake handler succeeded") // thread/name/set is not scripted
	}
}

func TestRestartReattaches(t *testing.T) {
	r := newRig(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := r.platform(ctx)
	res, err := first.EnsureRuntime(ctx, "/repo")
	if err != nil {
		t.Fatal(err)
	}
	first.Close() // the supervisor keeps running for the next dashboard
	second := r.platform(ctx)
	deadline := time.Now().Add(10 * time.Second)
	for second.current() == nil {
		if time.Now().After(deadline) {
			t.Fatal("persisted supervisor was not reattached")
		}
		time.Sleep(20 * time.Millisecond)
	}
	second.mu.Lock()
	endpoint := second.endpoint
	second.mu.Unlock()
	if endpoint != res.Endpoint {
		t.Fatalf("reattached %q, launched %q", endpoint, res.Endpoint)
	}
	if _, err := first.attach(ctx, res.Endpoint); err == nil {
		t.Fatal("closed platform attached")
	}
}

func TestHistory(t *testing.T) {
	r := newRig(t)
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	ctx := context.Background()
	p := r.platform(ctx)
	id, err := p.HistoryIdentity(ctx)
	canonical, _ := filepath.EvalSymlinks(home)
	if err != nil || id.Platform != "codex" || id.SourceKey != canonical || id.Name != "Codex" {
		t.Fatalf("identity = %+v %v", id, err)
	}
	inventory, err := p.HistoryInventory(ctx)
	if err != nil || len(inventory) != 1 || inventory[0].ID != "thr" {
		t.Fatalf("inventory = %+v %v", inventory, err)
	}
	snap, err := p.HistorySnapshot(ctx, "thr")
	if err != nil || snap.Session.Directory != "/repo" || len(snap.Messages) != 0 {
		t.Fatalf("snapshot = %+v %v", snap, err)
	}
	meta, err := p.SessionIdentityMetadata(ctx, "thr")
	if err != nil || meta.Title != "hello" {
		t.Fatalf("metadata = %+v %v", meta, err)
	}
	catalog, err := p.LiveDirectoryCatalog(ctx, platforms.DirectoryCatalogRequest{Directory: "/repo"})
	if err != nil || len(catalog.Models.Models) != 1 || catalog.Models.Models[0].Model != "gpt-test" {
		t.Fatalf("catalog = %+v %v", catalog, err)
	}
	if len(catalog.Commands) != 3 || catalog.Commands[2].Name != "pdf" || catalog.Commands[2].Source != "skill" {
		t.Fatalf("commands = %+v", catalog.Commands)
	}
	// A directory-less catalog still offers the built-in commands.
	if again, err := p.LiveDirectoryCatalog(ctx, platforms.DirectoryCatalogRequest{}); err != nil || again.Models.Models[0].Model != "gpt-test" || len(again.Commands) != 2 {
		t.Fatalf("directory-less catalog = %+v %v", again, err)
	}
	// Both lists are cached: a second read starts no app-server.
	p.history.mu.Lock()
	client, start := p.history.client, p.history.start
	p.history.client = nil
	p.history.start = func(context.Context) (*codexappserver.Client, error) { return nil, errors.New("not cached") }
	p.history.mu.Unlock()
	if _, err := p.LiveDirectoryCatalog(ctx, platforms.DirectoryCatalogRequest{Directory: "/repo"}); err != nil {
		t.Fatalf("cached catalog = %v", err)
	}
	p.history.mu.Lock()
	p.history.client, p.history.start = client, start
	p.history.mu.Unlock()
	account, err := p.Account(ctx)
	if err != nil || account.SubscriptionType != "plus" || account.RateLimits[0].Name != "five_hour" || account.RateLimits[0].Utilization != 0.1 {
		t.Fatalf("account = %+v %v", account, err)
	}
	if p.current() != nil {
		t.Fatal("history reads launched a supervisor")
	}
	sessions, err := p.Sessions(ctx, "", 0)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("sessions = %+v %v", sessions, err)
	}
	if sessions, _ := p.Sessions(ctx, "", 5000); len(sessions) != 0 {
		t.Fatalf("since filter = %+v", sessions)
	}
	// The reader restarts an app-server that exited.
	p.history.mu.Lock()
	_ = p.history.client.Close()
	p.history.mu.Unlock()
	<-p.history.client.Done()
	if _, err := p.HistoryInventory(ctx); err != nil {
		t.Fatalf("inventory after exit = %v", err)
	}
	t.Setenv("CODEX_HOME", filepath.Join(home, "missing"))
	if bound, err := p.HistoryIdentity(ctx); err != nil || bound != id {
		t.Fatalf("history identity changed after platform construction: %+v %v", bound, err)
	}
	if _, err := codexHome(); err == nil {
		t.Fatal("missing codex home accepted")
	}
	// Without CODEX_HOME the store is ~/.codex.
	user := t.TempDir()
	if err := os.Mkdir(filepath.Join(user, ".codex"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", "")
	t.Setenv("HOME", user)
	want, _ := filepath.EvalSymlinks(filepath.Join(user, ".codex"))
	if home, err := codexHome(); err != nil || home != want {
		t.Fatalf("default home = %q %v", home, err)
	}
}

func TestHistoryStartFailure(t *testing.T) {
	p := &Platform{history: &history{start: func(context.Context) (*codexappserver.Client, error) { return nil, errors.New("no codex") }}}
	ctx := context.Background()
	if _, err := p.HistoryInventory(ctx); !errors.Is(err, platforms.ErrHistoryUnavailable) {
		t.Fatalf("inventory = %v", err)
	}
	if _, err := p.HistorySnapshot(ctx, "x"); !errors.Is(err, platforms.ErrHistoryUnavailable) {
		t.Fatalf("snapshot = %v", err)
	}
	if _, err := p.SessionIdentityMetadata(ctx, "x"); !errors.Is(err, platforms.ErrHistoryUnavailable) {
		t.Fatalf("metadata = %v", err)
	}
	if _, err := p.Sessions(ctx, "", 0); !errors.Is(err, platforms.ErrHistoryUnavailable) {
		t.Fatalf("sessions = %v", err)
	}
	if _, err := p.LiveDirectoryCatalog(ctx, platforms.DirectoryCatalogRequest{}); !errors.Is(err, platforms.ErrHistoryUnavailable) {
		t.Fatalf("catalog = %v", err)
	}
	if _, err := p.Account(ctx); !errors.Is(err, platforms.ErrHistoryUnavailable) {
		t.Fatalf("account = %v", err)
	}
	p.history.close() // nothing started
}
