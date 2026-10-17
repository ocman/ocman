package claudecode

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/claudeprovider/claudetest"
	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/gitexec"
	"github.com/NoUseFreak/ocman/internal/ocruntime"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/providerprotocol"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/testutil"
)

// installFakeTmux puts the test-only tmux on bin and kills every provider it
// started when the test ends. The supervisor it launches is this test
// binary, which serves `provider <id>` from TestMain.
func installFakeTmux(t *testing.T, bin, pids string) {
	claudetest.InstallFakeTmux(t, bin, pids)
}

func TestFakeTmuxAliveDistinguishesZombie(t *testing.T) {
	data := t.TempDir()
	bin, pids := filepath.Join(data, "bin"), filepath.Join(data, "pids")
	installFakeTmux(t, bin, pids)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("OCMAN_FAKE_PROVIDER_PID_DIR", pids)
	runtime := ocruntime.NewBackendRuntime("claudecode", "", "")
	inst := &ocruntime.Instance{Kind: ocruntime.KindProviderTmux, ID: "lifecycle"}
	pidFile := filepath.Join(pids, inst.ID+".pid")
	writePID := func(pid int) {
		t.Helper()
		if err := os.WriteFile(pidFile, []byte(strconv.Itoa(pid)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// Synthetic handles must be removed before installFakeTmux's cleanup.
	t.Cleanup(func() { _ = os.Remove(pidFile) })
	writePID(os.Getpid())
	if alive, err := runtime.Alive(t.Context(), inst); err != nil || !alive {
		t.Fatalf("running process: alive=%v err=%v", alive, err)
	}

	// Keep the exited child unreaped, deterministically reproducing a container
	// PID1 that never waits for the fake tmux's orphaned supervisor.
	child := exec.Command("/bin/sh", "-c", "exit 0")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Wait() })
	writePID(child.Process.Pid)
	eventually(t, "unreaped child", func() bool {
		out, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(child.Process.Pid)).Output()
		return err == nil && strings.Contains(string(out), "Z")
	})
	if alive, err := runtime.Alive(t.Context(), inst); err != nil || alive {
		t.Fatalf("exited unreaped process: alive=%v err=%v", alive, err)
	}
	if err := child.Wait(); err != nil {
		t.Fatal(err)
	}
	if alive, err := runtime.Alive(t.Context(), inst); err != nil || alive {
		t.Fatalf("reaped process: alive=%v err=%v", alive, err)
	}
}

type recorded struct {
	mu     sync.Mutex
	events []providerprotocol.Event
	idles  map[string]int
}

func (r *recorded) has(typ, text string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.events {
		if e.Type == typ && strings.Contains(string(e.Properties), text) {
			return true
		}
	}
	return false
}

func (r *recorded) types(session string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, e := range r.events {
		if strings.Contains(string(e.Properties), session) && (e.Type != "message.part.updated" || strings.Contains(string(e.Properties), `"DONE"`)) {
			out = append(out, e.Type)
		}
	}
	return out
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(20 * time.Second); !ok(); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func readLines(path string) []string {
	data, _ := os.ReadFile(path)
	return strings.Fields(string(data))
}

func canonical(t *testing.T, path string) string {
	t.Helper()
	p, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// TestClaudeSupervisorSurvivesOcmanRestart drives a worktree session through
// the tmux-hosted project supervisor, stops ocman's side while a permission
// waits and again while the reply streams, and checks every restarted
// ocman recovers the same supervisor, the full turn and the prompt. A main
// checkout session shares that supervisor with its own cwd, and a dead
// supervisor is relaunched on the next send.
func TestClaudeSupervisorSurvivesOcmanRestart(t *testing.T) {
	data := testutil.ShortTempDir(t, "cr-") // short: unix socket path limit
	bin := filepath.Join(data, "bin")
	installFakeTmux(t, bin, filepath.Join(data, "pids"))
	claudetest.InstallBinary(t, filepath.Join(bin, "claude"))
	if err := os.MkdirAll(filepath.Join(data, "config", "projects"), 0o700); err != nil {
		t.Fatal(err)
	}
	hold, cwds, args := filepath.Join(data, "release"), filepath.Join(data, "cwd.log"), filepath.Join(data, "args.json")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("OCMAN_FAKE_PROVIDER_PID_DIR", filepath.Join(data, "pids"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(data, "config"))
	t.Setenv("FAKE_CLAUDE_FIXTURE", claudetest.Fixture("permission.jsonl"))
	t.Setenv("FAKE_CLAUDE_HOLD", hold)
	t.Setenv("FAKE_CLAUDE_CWD_LOG", cwds)
	t.Setenv("FAKE_CLAUDE_ARGS", args)

	repo, worktree := filepath.Join(data, "repo"), filepath.Join(data, "wt")
	for _, cmd := range [][]string{{"init", repo}, {"-C", repo, "-c", "user.name=T", "-c", "user.email=t@example.invalid", "commit", "--allow-empty", "-m", "x"},
		{"-C", repo, "worktree", "add", "-b", "wt", worktree}} {
		if out, err := gitexec.Command(t.Context(), cmd...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", cmd, err, out)
		}
	}
	repo, worktree = canonical(t, repo), canonical(t, worktree)
	stateDB, err := state.Open(filepath.Join(data, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stateDB.Close() })
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	// start is one ocman lifetime: platform, prompt pipeline and streams.
	start := func() (*claudePlatform, *recorded, context.CancelFunc) {
		ctx, cancel := context.WithCancel(t.Context())
		p := newClaudePlatformAt(ctx, nil, stateDB, data, exe, nil, "")
		if p == nil {
			t.Fatal("Claude platform did not start")
		}
		p.SetMCPAddr("127.0.0.1:47231")
		rec := &recorded{idles: map[string]int{}}
		p.SetEventHandler(func(e providerprotocol.Event) { rec.mu.Lock(); rec.events = append(rec.events, e); rec.mu.Unlock() })
		p.SetSessionEventSink(platforms.SessionEventSink{OnSessionIdle: func(_, id string) { rec.mu.Lock(); rec.idles[id]++; rec.mu.Unlock() }})
		go func() { _ = p.WatchEvents(ctx) }()
		return p, rec, cancel
	}
	pending := func(p *claudePlatform, id string) []platforms.LivePrompt {
		prompts, _ := p.ListPermissions(t.Context(), id)
		return prompts
	}
	endpoint := func() string {
		row, ok, err := stateDB.GetManagedProvider(t.Context(), "claudecode", repo)
		if err != nil || !ok {
			t.Fatal("no persisted supervisor", err)
		}
		return row.Endpoint
	}

	// First ocman: launch the project supervisor and stop at the permission.
	p1, _, stop1 := start()
	created, err := p1.CreateSession(t.Context(), platforms.CreateSessionRequest{Directory: worktree, Title: "wt"})
	if err != nil {
		t.Fatal(err)
	}
	id := created.ID
	if err := p1.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: id, Message: "go"}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "permission asked", func() bool { return len(pending(p1, id)) == 1 })
	rows, err := p1.Sessions(t.Context(), "", 0)
	if err != nil {
		t.Fatal(err)
	}
	rowIndex := slices.IndexFunc(rows, func(s db.Session) bool { return s.ID == id })
	if rowIndex < 0 || !rows[rowIndex].PendingPermission || rows[rowIndex].Status != db.StatusWaiting {
		t.Fatalf("background permission must appear as waiting in the session list: %+v", rows)
	}
	permission := fmt.Sprint(pending(p1, id)[0]["id"])
	first := endpoint()
	stop1()

	// Second ocman: the same supervisor is re-probed, the prompt replayed
	// and answerable; then stop again while the reply streams.
	p2, rec2, stop2 := start()
	eventually(t, "replayed permission", func() bool { return len(pending(p2, id)) == 1 })
	if got := fmt.Sprint(pending(p2, id)[0]["id"]); got != permission {
		t.Fatalf("replayed prompt %s, want %s", got, permission)
	}
	if err := p2.RespondPermission(t.Context(), platforms.RespondPermissionRequest{SessionID: id, PermissionID: permission, Reply: "once"}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "reply streaming", func() bool { return rec2.has("message.part.updated", `"DONE"`) })
	if endpoint() != first {
		t.Fatal("restart relaunched a live supervisor")
	}
	if s := p2.LiveStatus(id); s == nil || s.Status != string(db.StatusBusy) {
		t.Fatalf("mid-turn status = %+v", s)
	}
	stop2()

	// Third ocman: the unacknowledged turn replays whole and completes.
	p3, rec3, stop3 := start()
	defer stop3()
	eventually(t, "mid-turn replay", func() bool { return rec3.has("message.part.updated", `"DONE"`) })
	if err := os.WriteFile(hold, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	eventually(t, "turn completed", func() bool { rec3.mu.Lock(); defer rec3.mu.Unlock(); return rec3.idles[id] == 1 })
	want := []string{"session.status", "permission.asked", "permission.replied", "message.part.updated", "session.idle"}
	if got := slices.Compact(rec3.types(id)); !isSubsequence(want, got) {
		t.Fatalf("replayed turn %v, want %v in order", got, want)
	}
	if lines := readLines(cwds); len(lines) != 1 || canonical(t, lines[0]) != worktree {
		t.Fatalf("worktree child cwd = %v", lines)
	}

	// A main checkout session shares the project supervisor, in its own cwd,
	// with ocman's bound MCP address and skills.
	main, err := p3.CreateSession(t.Context(), platforms.CreateSessionRequest{Directory: repo})
	if err != nil {
		t.Fatal(err)
	}
	if err := p3.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: main.ID, Message: "go"}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "main checkout child", func() bool { return len(readLines(cwds)) == 2 })
	if lines := readLines(cwds); canonical(t, lines[1]) != repo {
		t.Fatalf("main child cwd = %v", lines)
	}
	if roots, _ := stateDB.ListManagedProviders(t.Context(), "claudecode"); len(roots) != 1 || endpoint() != first {
		t.Fatalf("worktree and main checkout got separate supervisors: %v", roots)
	}
	eventually(t, "main checkout prompt", func() bool { return len(pending(p3, main.ID)) == 1 })

	// Launched sessions read from their project supervisor; terminal
	// sessions stay with the browsing supervisor until their first send.
	ctx := t.Context()
	if !p3.Owns(ctx, main.ID) || p3.Owns(ctx, "terminal") {
		t.Fatal("ownership")
	}
	if d, err := p3.Session(ctx, main.ID, 10, 0); err != nil || d.Session.ID != main.ID {
		t.Fatal(d, err)
	}
	if l, err := p3.SessionLifecycle(ctx, main.ID); err != nil || l.Status != db.StatusBusy {
		t.Fatal(l, err)
	}
	listed, _ := p3.Sessions(ctx, "", 0)
	if i := slices.IndexFunc(listed, func(s db.Session) bool { return s.ID == main.ID }); i < 0 || listed[i].Status != db.StatusWaiting || !listed[i].PendingPermission {
		t.Fatalf("list = %+v", listed)
	}
	if refreshed, err := p3.RefreshPermissions(ctx, main.ID); err != nil || len(refreshed) != 1 {
		t.Fatal(refreshed, err)
	}
	if q, err := p3.ListQuestions(ctx, main.ID); err != nil || len(q) != 0 {
		t.Fatal(q, err)
	}
	if q, err := p3.RefreshQuestions(ctx, main.ID); err != nil || len(q) != 0 {
		t.Fatal(q, err)
	}
	if err := p3.RenameSession(ctx, platforms.RenameSessionRequest{SessionID: main.ID, Title: "Main"}); err != nil {
		t.Fatal(err)
	}
	if err := p3.ExecuteCommand(ctx, platforms.ExecuteCommandRequest{SessionID: "terminal", Command: "x"}); err == nil {
		t.Fatal("unknown session driven")
	}
	if err := p3.Compact(ctx, platforms.CompactRequest{SessionID: "terminal"}); err == nil {
		t.Fatal("unknown session compacted")
	}
	if _, err := p3.ForkSession(ctx, platforms.ForkSessionRequest{SessionID: "terminal"}); err == nil {
		t.Fatal("unknown session forked")
	}
	if err := p3.RespondQuestion(ctx, platforms.RespondQuestionRequest{SessionID: main.ID, RequestID: "nope"}); err == nil {
		t.Fatal("unknown question answered")
	}
	if err := p3.RejectQuestion(ctx, platforms.RejectQuestionRequest{SessionID: main.ID, RequestID: "nope"}); err == nil {
		t.Fatal("unknown question rejected")
	}
	if err := p3.Abort(ctx, platforms.AbortRequest{SessionID: "terminal"}); err != nil {
		t.Fatal(err)
	}

	var argv []string
	raw, _ := os.ReadFile(args)
	_ = json.Unmarshal(raw, &argv)
	if !strings.Contains(argv[slices.Index(argv, "--mcp-config")+1], "http://127.0.0.1:47231/mcp") ||
		!strings.HasPrefix(argv[slices.Index(argv, "--plugin-dir")+1], filepath.Join(data, "claude-marketplace")) {
		t.Fatalf("child args = %q", argv)
	}

	// A dead persisted supervisor is relaunched by the next send, and the
	// session resumes there in its worktree.
	row, _, _ := stateDB.GetManagedProvider(t.Context(), "claudecode", repo)
	runtime := p3.projects.mgr.Runtime
	inst := &ocruntime.Instance{Endpoint: row.Endpoint, Kind: row.Kind, ID: row.RuntimeID}
	if err := runtime.Stop(t.Context(), inst); err != nil {
		t.Fatal(err)
	}
	eventually(t, "supervisor gone", func() bool { alive, err := runtime.Alive(t.Context(), inst); return err == nil && !alive })
	if err := p3.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: id, Message: "again"}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "relaunched supervisor asks", func() bool { return len(pending(p3, id)) == 1 })
	if endpoint() == first {
		t.Fatal("dead supervisor not replaced")
	}
	if lines := readLines(cwds); canonical(t, lines[len(lines)-1]) != worktree {
		t.Fatalf("resumed child cwd = %v", lines)
	}
	raw, _ = os.ReadFile(args)
	_ = json.Unmarshal(raw, &argv)
	if argv[slices.Index(argv, "--resume")+1] != id {
		t.Fatalf("relaunched child args = %q", argv)
	}

	// Before its supervisor is attached, a launched session streams from
	// the browsing supervisor until the attach, then ends to reconnect.
	detached := newClaudePlatformAt(ctx, nil, nil, data, exe, nil, "")
	browsing := make(chan struct{})
	hook := claudeTestHook
	claudeTestHook = func(point, key string) {
		if point == "browsing" && key == id {
			close(browsing)
		}
	}
	t.Cleanup(func() { claudeTestHook = hook })
	done := make(chan error, 1)
	go func() { done <- detached.ProxyEvents(ctx, id, io.Discard, func() {}) }()
	<-browsing // routed to the browsing stream: nothing is attached yet
	time.Sleep(100 * time.Millisecond)
	select {
	case err := <-done:
		t.Fatal("stream ended before attach", err)
	default:
	}
	if _, err := detached.projects.attach(ctx, repo, endpoint()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("stream not ended by attach")
	}
	if _, err := detached.EnsureRuntime(ctx, worktree); err == nil {
		t.Fatal("ensure without state")
	}
}

func isSubsequence(want, got []string) bool {
	i := 0
	for _, g := range got {
		if i < len(want) && g == want[i] {
			i++
		}
	}
	return i == len(want)
}
