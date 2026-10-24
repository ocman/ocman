package claudecode

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/gitexec"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/claude/provider/claudetest"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/testutil"
)

// recoveryEnv is a short data directory with the fake tmux and claude on
// PATH, a git repository and an isolated state.db.
type recoveryEnv struct {
	data, bin, repo string
	state           *state.DB
	exe             string
}

func newRecoveryEnv(t *testing.T) *recoveryEnv {
	t.Helper()
	data := testutil.ShortTempDir(t, "rv-") // short: unix socket path limit
	e := &recoveryEnv{data: data, bin: filepath.Join(data, "bin"), repo: filepath.Join(data, "repo")}
	installFakeTmux(t, e.bin, filepath.Join(data, "pids"))
	claudetest.InstallBinary(t, filepath.Join(e.bin, "claude"))
	for _, cmd := range [][]string{{"init", e.repo}, {"-C", e.repo, "-c", "user.name=T", "-c", "user.email=t@example.invalid", "commit", "--allow-empty", "-m", "x"}} {
		if out, err := gitexec.Command(t.Context(), cmd...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", cmd, err, out)
		}
	}
	e.repo = canonical(t, e.repo)
	var err error
	if e.state, err = state.Open(filepath.Join(data, "state.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.state.Close() })
	if e.exe, err = os.Executable(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", e.bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("OCMAN_FAKE_PROVIDER_PID_DIR", filepath.Join(data, "pids"))
	return e
}

// start is one dashboard lifetime.
func (e *recoveryEnv) start(t *testing.T, mcp string) (*claudePlatform, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	p := newClaudePlatformAt(ctx, nil, e.state, e.data, e.exe, nil, "")
	if p == nil {
		cancel()
		t.Fatal("Claude platform did not start")
	}
	p.SetMCPAddr(mcp)
	go func() { _ = p.WatchEvents(ctx) }()
	return p, cancel
}

func (e *recoveryEnv) endpoint(t *testing.T) string {
	t.Helper()
	row, ok, err := e.state.GetManagedProvider(t.Context(), "claudecode", e.repo)
	if err != nil || !ok {
		return ""
	}
	return row.Endpoint
}

// turnsFixture answers n turns on one child, each echoed and completed.
func turnsFixture(t *testing.T, n int) string {
	t.Helper()
	var rows strings.Builder
	for i := 1; i <= n; i++ {
		n := strconv.Itoa(i)
		rows.WriteString(`{"direction":"stdin","message":{"type":"user"}}
{"direction":"stdout","message":{"type":"user","isReplay":true,"uuid":"$input:` + n + `","message":{"role":"user","content":"x"}}}
{"direction":"stdout","message":{"type":"assistant","message":{"id":"msg_` + n + `","model":"haiku","content":[{"type":"text","text":"OK"}]}}}
{"direction":"stdout","message":{"type":"result","subtype":"success","is_error":false,"result":"OK"}}
`)
	}
	path := filepath.Join(t.TempDir(), "turns.jsonl")
	if err := os.WriteFile(path, []byte(rows.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// Project supervisors run under a tmux server whose environment predates
// the dashboard: they still use the dashboard's claude and active
// CLAUDE_CONFIG_DIR. A dashboard restart that rebinds the MCP listener keeps
// the supervisor and its running turn, and the next child uses the new
// address; a changed Claude configuration drains and replaces it.
func TestProjectSupervisorFollowsDashboardConfiguration(t *testing.T) {
	e := newRecoveryEnv(t)
	hold, args, envLog, life := filepath.Join(e.data, "hold"), filepath.Join(e.data, "args.json"), filepath.Join(e.data, "env.log"), filepath.Join(e.data, "life.log")
	fake := []string{"FAKE_CLAUDE_FIXTURE=" + turnsFixture(t, 4), "FAKE_CLAUDE_HOLD=" + hold,
		"FAKE_CLAUDE_ARGS=" + args, "FAKE_CLAUDE_ENV_LOG=" + envLog, "FAKE_CLAUDE_LIFE_LOG=" + life, "FAKE_CLAUDE_VALIDATE_RESUME=1"}
	for _, kv := range fake {
		k, v, _ := strings.Cut(kv, "=")
		t.Setenv(k, v)
	}
	// The tmux server's environment: an old config, no claude on PATH.
	stale := filepath.Join(e.data, "tmux.env")
	if err := os.WriteFile(stale, []byte(strings.Join(append(fake, "PATH=/usr/bin:/bin", "HOME="+e.data, "CLAUDE_CONFIG_DIR="+filepath.Join(e.data, "stale")), "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OCMAN_FAKE_TMUX_ENV", stale)
	first := filepath.Join(e.data, "config-a")
	t.Setenv("CLAUDE_CONFIG_DIR", first)
	mcpOf := func() string {
		var argv []string
		data, _ := os.ReadFile(args)
		_ = json.Unmarshal(data, &argv)
		if i := slices.Index(argv, "--mcp-config"); i >= 0 {
			return argv[i+1]
		}
		return ""
	}
	starts := func() int { data, _ := os.ReadFile(life); return strings.Count(string(data), "start") }
	lastEnv := func() string { lines := readLines(envLog); return lines[len(lines)-1] }

	p, stop := e.start(t, "127.0.0.1:47311")
	created, err := p.CreateSession(t.Context(), platforms.CreateSessionRequest{Directory: e.repo})
	if err != nil {
		t.Fatal(err)
	}
	id := created.ID
	if err := os.WriteFile(hold, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := p.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: id, Message: "one"}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "first child", func() bool { return len(readLines(envLog)) == 1 })
	if lastEnv() != first || !strings.Contains(mcpOf(), "127.0.0.1:47311") {
		t.Fatalf("child config = %q mcp = %q, want the dashboard's", lastEnv(), mcpOf())
	}
	supervisor := e.endpoint(t)

	// Hold the next turn open across a restart that rebinds the listener.
	// Only after the first turn idles: the fake waits at a result row while
	// hold is absent, and the child logs its environment before reaching it.
	eventually(t, "first turn idle", func() bool { s := p.LiveStatus(id); return s != nil && s.Status == "done" })
	if err := os.Remove(hold); err != nil {
		t.Fatal(err)
	}
	if err := p.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: id, Message: "two"}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "second turn running", func() bool { s := p.LiveStatus(id); return s != nil && s.Status == "busy" })
	stop()
	p, stop = e.start(t, "127.0.0.1:47312")
	eventually(t, "reattached mid-turn", func() bool { s := p.LiveStatus(id); return s != nil && s.Status == "busy" })
	if err := os.WriteFile(hold, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	eventually(t, "held turn finished", func() bool { s := p.LiveStatus(id); return s != nil && s.Status == "done" })
	if e.endpoint(t) != supervisor || starts() != 1 {
		t.Fatalf("the rebind replaced the supervisor or interrupted its turn: endpoint %s, %d children", e.endpoint(t), starts())
	}
	if err := p.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: id, Message: "three"}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "child relaunched with the new listener", func() bool { return strings.Contains(mcpOf(), "127.0.0.1:47312") })
	eventually(t, "third turn idle", func() bool { s := p.LiveStatus(id); return s != nil && s.Status == "done" })
	stop()

	// Another Claude configuration: the settled supervisor is replaced. The
	// old session's history is in the old configuration, so it is refused
	// visibly rather than resumed against the new one (the fake validates
	// --resume against the active configuration's history); a new session
	// runs under the new configuration, and switching back drives the old.
	second := filepath.Join(e.data, "config-b")
	t.Setenv("CLAUDE_CONFIG_DIR", second)
	p, stop = e.start(t, "127.0.0.1:47312")
	eventually(t, "supervisor replaced", func() bool { return e.endpoint(t) != "" && e.endpoint(t) != supervisor })
	eventually(t, "old session unavailable under the new configuration", func() bool {
		err := p.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: id, Message: "four"})
		return err != nil && strings.Contains(err.Error(), "unavailable under")
	})
	fresh, err := p.CreateSession(t.Context(), platforms.CreateSessionRequest{Directory: e.repo})
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "new session runs", func() bool {
		return p.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: fresh.ID, Message: "one"}) == nil
	})
	eventually(t, "child under the new configuration", func() bool { return lastEnv() == second })
	eventually(t, "new session idle", func() bool { s := p.LiveStatus(fresh.ID); return s != nil && s.Status == "done" })
	stop()

	t.Setenv("CLAUDE_CONFIG_DIR", first)
	p, stop = e.start(t, "127.0.0.1:47312")
	defer stop()
	eventually(t, "old session resumes under its own configuration", func() bool {
		return p.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: id, Message: "five"}) == nil
	})
	eventually(t, "resumed turn idle", func() bool { s := p.LiveStatus(id); return s != nil && s.Status == "done" })
	if lastEnv() != first {
		t.Fatalf("old session ran under %s", lastEnv())
	}
}

// After a dashboard restart, a fresh list, detail and lifecycle read the
// failed status (including usage-limit failures) the project supervisor
// settled, not the transcript's inference, though the turns were
// acknowledged and are no longer replayed.
func TestRestartedDashboardKeepsSettledStatus(t *testing.T) {
	e := newRecoveryEnv(t)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(e.data, "config"))
	resets := time.Now().Add(time.Hour).Unix()
	fixture := filepath.Join(e.data, "fixture.jsonl")
	if err := os.WriteFile(fixture, []byte(`{"direction":"stdin","message":{"type":"user"}}
{"direction":"stdout","message":{"type":"result","subtype":"success","is_error":true,"result":"API Error: 529 overloaded"}}
{"direction":"stdin","message":{"type":"user"}}
{"direction":"stdout","message":{"type":"rate_limit_event","rate_limit_info":{"status":"rejected","resetsAt":`+strconv.FormatInt(resets, 10)+`,"rateLimitType":"five_hour"}}}
{"direction":"stdout","message":{"type":"result","is_error":true,"subtype":"success","result":"Claude usage limit reached"}}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_CLAUDE_FIXTURE", fixture)
	read := func(p *claudePlatform, id string) (listed, detail, life db.Session) {
		sessions, _ := p.Sessions(t.Context(), "", 0)
		if i := slices.IndexFunc(sessions, func(s db.Session) bool { return s.ID == id }); i >= 0 {
			listed = sessions[i]
		}
		if d, err := runtimeDetail(t.Context(), p, id, 10, 0); err == nil {
			detail = *d.Session
		}
		if l, err := p.SessionLifecycle(t.Context(), id); err == nil {
			life.Status = l.Status
		}
		return
	}
	p, stop := e.start(t, "127.0.0.1:47321")
	created, err := p.CreateSession(t.Context(), platforms.CreateSessionRequest{Directory: e.repo})
	if err != nil {
		t.Fatal(err)
	}
	id := created.ID
	for range 2 {
		want := db.StatusError
		idled := make(chan struct{}, 1)
		p.SetSessionEventSink(platforms.SessionEventSink{OnSessionIdle: func(_, sid string) {
			if sid == id {
				idled <- struct{}{}
			}
		}})
		if err := p.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: id, Message: "go"}); err != nil {
			t.Fatal(err)
		}
		select {
		case <-idled:
		case <-time.After(10 * time.Second):
			t.Fatal("finished turn did not idle before quota reset")
		}
		eventually(t, "turn settled", func() bool { s := p.LiveStatus(id); return s != nil && s.Status == string(want) })
		stop()
		p, stop = e.start(t, "127.0.0.1:47321")
		var listed, detail, life db.Session
		eventually(t, "restarted reads", func() bool {
			listed, detail, life = read(p, id)
			return listed.Status == want && detail.Status == want && life.Status == want
		})
		if listed.ResetsAt != 0 || detail.ResetsAt != 0 {
			t.Fatalf("quota reset leaked into session state: list %d detail %d", listed.ResetsAt, detail.ResetsAt)
		}
	}
	stop()
}
