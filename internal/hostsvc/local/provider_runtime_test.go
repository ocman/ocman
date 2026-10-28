package local

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/gitexec"
	"github.com/ocman/ocman/internal/ocruntime"
	"github.com/ocman/ocman/internal/providerprotocol"
	"github.com/ocman/ocman/internal/providersupervisor/providertest"
	"github.com/ocman/ocman/internal/state"
)

func providerManager(t *testing.T) (*ProviderManager, string, string) {
	t.Helper()
	dir := providertest.Directory(t)
	fixture, err := os.ReadFile("testdata/provider-tmux")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tmux"), fixture, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("OCMAN_FAKE_PROVIDER_PID_DIR", dir)
	// macOS validates a newly written executable on its first exec, measured at
	// 0.1-0.4s idle and past tmux's 2s command deadline under coverage load.
	// Pay that here, outside the deadline; "has-session" on no pid exits 1.
	_ = exec.Command(filepath.Join(dir, "tmux"), "-L", "ocman-agents", "-f", "/dev/null", "has-session", "-t", "warm").Run()
	db, err := state.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	binary := providertest.Binary(t)
	m := &ProviderManager{Runtime: ocruntime.NewBackendRuntime("fake", binary, dir), State: db}
	t.Cleanup(func() {
		paths, _ := filepath.Glob(filepath.Join(dir, "*.pid"))
		for _, path := range paths {
			data, _ := os.ReadFile(path)
			pid, _ := strconv.Atoi(string(data))
			if pid > 0 {
				process, _ := os.FindProcess(pid)
				_ = process.Kill()
			}
		}
	})
	root := filepath.Join(dir, "repo")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	if output, err := gitexec.Command(t.Context(), "-C", root, "init").CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, output)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	return m, root, dir
}

func TestProviderDeadPersistedRowIsReprobedAndRelaunched(t *testing.T) {
	m, root, dir := providerManager(t)
	if err := m.State.UpsertManagedProvider(t.Context(), "fake", root, state.ManagedInstance{Endpoint: "unix://" + filepath.Join(dir, "dead"), Kind: ocruntime.KindProviderTmux, RuntimeID: "dead"}); err != nil {
		t.Fatal(err)
	}
	result, err := m.ensureRoot(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Launched || result.Runtime.ID == "dead" {
		t.Fatal(result)
	}
	row, ok, err := m.State.GetManagedProvider(t.Context(), "fake", root)
	if err != nil || !ok || row.Endpoint != result.Endpoint {
		t.Fatal(row, ok, err)
	}
	// A fresh dashboard manager has no memory of the launch and must probe the
	// persisted socket, reusing only the live instance.
	restarted := &ProviderManager{Runtime: m.Runtime, State: m.State}
	again, err := restarted.Ensure(t.Context(), root)
	if err != nil || again.Launched || again.Endpoint != result.Endpoint {
		t.Fatal(again, err)
	}
	if output, err := gitexec.Command(t.Context(), "-C", root, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "fixture").CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, output)
	}
	linked := filepath.Join(dir, "linked")
	if output, err := gitexec.Command(t.Context(), "-C", root, "worktree", "add", "-b", "linked", linked).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, output)
	}
	worktree, err := restarted.Ensure(t.Context(), linked)
	if err != nil || worktree.Launched || worktree.Endpoint != result.Endpoint {
		t.Fatal("worktree launched separate supervisor", worktree, err)
	}
	// Recovery probes again, rather than treating a previously live row as proof.
	if err := m.Runtime.Stop(t.Context(), &result.Runtime); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	// Closing the socket precedes process exit. ensureRoot correctly refuses
	// to replace a still-alive supervisor whose API is already shutting down.
	for time.Now().Before(deadline) {
		alive, err := m.Runtime.Alive(t.Context(), &result.Runtime)
		if err != nil {
			t.Fatal(err)
		}
		if !alive {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	newResult, err := restarted.ensureRoot(t.Context(), root)
	if err != nil || !newResult.Launched || newResult.Endpoint == result.Endpoint {
		t.Fatal(newResult, err)
	}
}

func TestProviderVersionSkewDrainsWithoutInterruptingTurn(t *testing.T) {
	m, root, dir := providerManager(t)
	old := providertest.Start(t, m.Runtime.Binary, providerprotocol.CurrentVersion+10)
	props := json.RawMessage(`{"sessionID":"s","status":{"type":"busy"}}`)
	if status := providertest.Call(t, old.Client, "POST", "/test/event", providerprotocol.Event{Type: "session.status", Properties: props}, nil); status != 204 {
		t.Fatal(status)
	}
	// Fake tmux's persisted handle addresses the old fake process.
	if err := os.WriteFile(filepath.Join(dir, "old.pid"), []byte(strconv.Itoa(old.Cmd.Process.Pid)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.State.UpsertManagedProvider(t.Context(), "fake", root, state.ManagedInstance{Endpoint: "unix://" + old.Socket, Kind: ocruntime.KindProviderTmux, RuntimeID: "old"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := m.ensureRoot(ctx, root); done <- err }()
	// /drain both announces draining and rejects new mutations while the old
	// turn remains alive. Pending permissions are still replyable.
	var drain providerprotocol.DrainState
	if status := providertest.Call(t, old.Client, "POST", "/drain", nil, &drain); status != 200 || drain.Idle {
		t.Fatal(drain, status)
	}
	select {
	case err := <-done:
		t.Fatal("replaced busy supervisor", err)
	case <-time.After(100 * time.Millisecond):
	}
	if status := providertest.Call(t, old.Client, "POST", "/sessions", nil, nil); status != 409 {
		t.Fatal(status)
	}
	// Test control uses the reply route, which remains admitted during drain.
	if status := providertest.Call(t, old.Client, "POST", "/permissions/p/reply", nil, nil); status != 204 {
		t.Fatal(status)
	}
	// An idle edge originates in the running child, so emit through the fake's
	// GET control hook rather than a new session mutation.
	resp, err := old.Client.Get("http://provider/test/idle")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("idle supervisor not replaced")
	}
	row, _, err := m.State.GetManagedProvider(t.Context(), "fake", root)
	if err != nil || strings.Contains(row.Endpoint, old.Socket) {
		t.Fatal(row, err)
	}
}

func TestProviderManagerErrors(t *testing.T) {
	if _, err := (&ProviderManager{}).ensureRoot(t.Context(), "/repo"); err == nil {
		t.Fatal("missing dependencies accepted")
	}
	if _, _, err := (&ProviderManager{}).Recover(t.Context(), "/repo"); err == nil {
		t.Fatal("missing dependencies accepted")
	}
	// Outside a repository the canonical directory is the root.
	dir := t.TempDir()
	canonical, _ := filepath.EvalSymlinks(dir)
	if root, err := ProviderRoot(t.Context(), dir); err != nil || root != canonical {
		t.Fatal(root, err)
	}
	for _, bad := range []string{"relative", filepath.Join(dir, "missing")} {
		if _, err := ProviderRoot(t.Context(), bad); err == nil {
			t.Fatal(bad)
		}
	}
	file := filepath.Join(dir, "file")
	_ = os.WriteFile(file, nil, 0o600)
	if _, err := ProviderRoot(t.Context(), file); err == nil {
		t.Fatal("file accepted")
	}
}

// Recover reattaches a live persisted supervisor, never launches for a dead
// one, and replaces an idle out-of-range one.
func TestProviderRecover(t *testing.T) {
	m, root, dir := providerManager(t)
	if result, ok, err := m.Recover(t.Context(), root); err != nil || ok || result != nil {
		t.Fatal("recovered without a row", result, err)
	}
	if err := m.State.UpsertManagedProvider(t.Context(), "fake", root, state.ManagedInstance{Endpoint: "unix://" + filepath.Join(dir, "dead"), Kind: ocruntime.KindProviderTmux, RuntimeID: "dead"}); err != nil {
		t.Fatal(err)
	}
	if result, ok, err := m.Recover(t.Context(), root); err != nil || ok || result != nil {
		t.Fatal("dead row recovered", result, err)
	}
	if row, ok, _ := m.State.GetManagedProvider(t.Context(), "fake", root); !ok || row.RuntimeID != "dead" {
		t.Fatal("recover launched for a dead row", row)
	}
	launched, err := m.ensureRoot(t.Context(), root)
	if err != nil || !launched.Launched {
		t.Fatal(launched, err)
	}
	restarted := &ProviderManager{Runtime: m.Runtime, State: m.State}
	again, ok, err := restarted.Recover(t.Context(), root)
	if err != nil || !ok || again.Launched || again.Endpoint != launched.Endpoint {
		t.Fatal(again, ok, err)
	}
	old := providertest.Start(t, m.Runtime.Binary, providerprotocol.CurrentVersion+10)
	if err := os.WriteFile(filepath.Join(dir, "old.pid"), []byte(strconv.Itoa(old.Cmd.Process.Pid)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.State.UpsertManagedProvider(t.Context(), "fake", root, state.ManagedInstance{Endpoint: "unix://" + old.Socket, Kind: ocruntime.KindProviderTmux, RuntimeID: "old"}); err != nil {
		t.Fatal(err)
	}
	replaced, ok, err := restarted.Recover(t.Context(), root)
	if err != nil || !ok || !replaced.Launched || replaced.Endpoint == "unix://"+old.Socket {
		t.Fatal(replaced, ok, err)
	}
}

func TestUncertainProbePreservesLiveSupervisor(t *testing.T) {
	m, root, dir := providerManager(t)
	socket := filepath.Join(dir, "unhealthy")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) })}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	pidPath := filepath.Join(dir, "live.pid")
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
		t.Fatal(err)
	}
	// Remove this synthetic handle before the fixture cleans up child pids.
	t.Cleanup(func() { _ = os.Remove(pidPath) })
	endpoint := "unix://" + socket
	if err := m.State.UpsertManagedProvider(t.Context(), "fake", root, state.ManagedInstance{Endpoint: endpoint, Kind: ocruntime.KindProviderTmux, RuntimeID: "live"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ensureRoot(t.Context(), root); err == nil {
		t.Fatal("unhealthy live supervisor reused")
	}
	row, ok, err := m.State.GetManagedProvider(t.Context(), "fake", root)
	if err != nil || !ok || row.Endpoint != endpoint {
		t.Fatal("live row replaced after uncertain probe", row, err)
	}
	paths, err := filepath.Glob(filepath.Join(dir, "*.pid"))
	if err != nil || len(paths) != 1 {
		t.Fatal("second supervisor launched", paths, err)
	}
}
