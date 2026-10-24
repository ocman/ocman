package claudecode

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/NoUseFreak/ocman/internal/hostsvc/local"
	"github.com/NoUseFreak/ocman/internal/ocruntime"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

func TestFailedSupervisorProbeRequiresIndependentDeathProof(t *testing.T) {
	e := newRecoveryEnv(t)
	runtime := ocruntime.NewBackendRuntime("claudecode", "", "")
	failed, socket := reloadTestAdapter(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	})
	endpoint := "unix://" + socket
	c := &claudeProjects{mgr: &local.ProviderManager{State: e.state, Runtime: runtime}, roots: map[string]*claudeProject{
		e.repo: {watched: watched{adapter: failed}, endpoint: endpoint},
	}}
	if c.runtimeDefinitelyStopped(t.Context(), failed) {
		t.Fatal("untracked supervisor was assumed dead")
	}
	tracked := state.ManagedInstance{Endpoint: endpoint, Kind: ocruntime.KindProviderTmux, RuntimeID: "routing-proof"}
	if err := e.state.UpsertManagedProvider(t.Context(), "claudecode", e.repo, tracked); err != nil {
		t.Fatal(err)
	}
	pidFile := filepath.Join(e.data, "pids", "routing-proof.pid")
	// As in TestFakeTmuxAliveDistinguishesZombie, remove the synthetic handle
	// before the fake tmux cleanup runs; it must never signal this test process.
	t.Cleanup(func() { _ = os.Remove(pidFile) })
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
		t.Fatal(err)
	}
	if c.runtimeDefinitelyStopped(t.Context(), failed) {
		t.Fatal("failed API probe hid a live tracked process")
	}
	if _, err := c.findLiveSession(t.Context(), "native"); !errors.Is(err, platforms.ErrPlatformUnreachable) {
		t.Fatalf("live-but-unreachable supervisor authorized fallback: %v", err)
	}
	if err := os.Remove(pidFile); err != nil {
		t.Fatal(err)
	}
	if !c.runtimeDefinitelyStopped(t.Context(), failed) {
		t.Fatal("independently confirmed process death was not recognized")
	}
	if owner, err := c.findLiveSession(t.Context(), "native"); err != nil || owner != nil {
		t.Fatalf("proven dead runtime prevented safe recovery: %v", err)
	}
	tracked.Endpoint = "unix:///replacement"
	if err := e.state.UpsertManagedProvider(t.Context(), "claudecode", e.repo, tracked); err != nil {
		t.Fatal(err)
	}
	if c.runtimeDefinitelyStopped(t.Context(), failed) {
		t.Fatal("replacement runtime's identity was borrowed as death proof")
	}
	delete(c.roots, e.repo)
	if c.runtimeDefinitelyStopped(t.Context(), failed) {
		t.Fatal("detached adapter was treated as a known dead runtime")
	}
}
