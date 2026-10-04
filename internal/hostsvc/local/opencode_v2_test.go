package local

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/ocv2"
)

// portRecorder captures Deps.SetMachineServer publications.
type portRecorder struct {
	mu    sync.Mutex
	ports []string
}

// TestMain pins the installed-OpenCode check so no test runs the real
// `opencode --version`.
func TestMain(m *testing.M) {
	ocv2.SetInstalledV2(false)
	os.Exit(m.Run())
}

func (p *portRecorder) set(port string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ports = append(p.ports, port)
}

func (p *portRecorder) last() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.ports) == 0 {
		return "<none>"
	}
	return p.ports[len(p.ports)-1]
}

func v2Host(t *testing.T) (*Host, *fakeRuntime, *fakeStore, *portRecorder, string) {
	t.Helper()
	t.Cleanup(ocv2.SetInstalledV2(true))
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OPENCODE_DB", "")
	rt := &fakeRuntime{endpoint: "http://127.0.0.1:7777"}
	store := newFakeStore()
	rec := &portRecorder{}
	h := New(Deps{Runtime: rt, ManagedStore: store, SetMachineServer: rec.set})
	return h, rt, store, rec, filepath.Join(home, ".local", "share", "ocman", "opencode-v2")
}

func TestV2EnsureLaunchesOneMachineServer(t *testing.T) {
	h, rt, store, rec, root := v2Host(t)
	ctx := context.Background()
	repoA, repoB := initRepo(t), initRepo(t)

	res, err := h.EnsureProjectOpencode(ctx, hostsvc.EnsureProjectOpencodeRequest{ProjectDir: repoA})
	if err != nil {
		t.Fatalf("ensure A: %v", err)
	}
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		t.Fatalf("machine root %s not created: %v", root, err)
	}
	spec := rt.spec()
	if !spec.V2 {
		t.Error("LaunchSpec.V2 = false; want true")
	}
	if spec.RepoRoot != root {
		t.Errorf("launched RepoRoot = %q; want machine root %q", spec.RepoRoot, root)
	}
	if !store.has(root) {
		t.Errorf("store not keyed by machine root %q", root)
	}
	wantA, _ := projectOpencodeRoot(ctx, repoA)
	if res.RepoRoot != wantA {
		t.Errorf("RepoRoot = %q; want project root %q", res.RepoRoot, wantA)
	}
	if got := rec.last(); got != "7777" {
		t.Errorf("SetMachineServer got %q; want 7777", got)
	}

	resB, err := h.EnsureProjectOpencode(ctx, hostsvc.EnsureProjectOpencodeRequest{ProjectDir: repoB})
	if err != nil {
		t.Fatalf("ensure B: %v", err)
	}
	if rt.launchCount() != 1 {
		t.Errorf("launches = %d; want 1 (machine server reused)", rt.launchCount())
	}
	wantB, _ := projectOpencodeRoot(ctx, repoB)
	if resB.RepoRoot != wantB || resB.Endpoint != res.Endpoint {
		t.Errorf("second result = %+v; want RepoRoot %q on endpoint %q", resB, wantB, res.Endpoint)
	}

	plain := t.TempDir()
	resP, err := h.EnsureProjectOpencode(ctx, hostsvc.EnsureProjectOpencodeRequest{ProjectDir: plain})
	if err != nil {
		t.Fatalf("ensure non-repo: %v", err)
	}
	if resP.RepoRoot != plain {
		t.Errorf("non-repo RepoRoot = %q; want %q", resP.RepoRoot, plain)
	}
}

func TestV2RestartAndStopMachineServer(t *testing.T) {
	h, rt, _, rec, root := v2Host(t)
	ctx := context.Background()
	repo := initRepo(t)

	if _, err := h.EnsureProjectOpencode(ctx, hostsvc.EnsureProjectOpencodeRequest{ProjectDir: repo}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.RestartProjectOpencode(ctx, hostsvc.EnsureProjectOpencodeRequest{ProjectDir: repo}); err != nil {
		t.Fatalf("restart: %v", err)
	}
	if rt.launchCount() != 2 || rt.stopCount() != 1 {
		t.Errorf("after restart launches=%d stops=%d; want 2/1", rt.launchCount(), rt.stopCount())
	}
	if rt.spec().RepoRoot != root {
		t.Errorf("restart launched %q; want machine root", rt.spec().RepoRoot)
	}

	// A project-scoped stop (project archive) must leave the shared server
	// running: other projects may be mid-turn on it.
	if err := h.StopProjectOpencode(ctx, hostsvc.EnsureProjectOpencodeRequest{ProjectDir: repo}); err != nil {
		t.Fatalf("project stop: %v", err)
	}
	if rt.stopCount() != 1 || rec.last() == "" {
		t.Fatalf("project stop stopped the shared server: stops=%d published=%q", rt.stopCount(), rec.last())
	}
	// The managed inventory reports the shared server as machine-scoped.
	inv, err := h.ManagedOpencodes(ctx)
	if err != nil || len(inv) != 1 || !inv[0].Machine {
		t.Fatalf("inventory = %+v, %v; want one machine-scoped instance", inv, err)
	}
	// Only a machine-scoped stop tears it down.
	if err := h.StopProjectOpencode(ctx, hostsvc.EnsureProjectOpencodeRequest{ProjectDir: inv[0].RepoRoot}); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if rt.stopCount() != 2 {
		t.Errorf("stops = %d; want 2", rt.stopCount())
	}
	if got := rec.last(); got != "" {
		t.Errorf("SetMachineServer after stop = %q; want \"\"", got)
	}
}

func runSupervisorBriefly(t *testing.T, h *Host, rt *fakeRuntime) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { h.RunMachineSupervisor(ctx); close(done) }()
	deadline := time.Now().Add(200 * time.Millisecond)
	for rt.launchCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RunMachineSupervisor did not return after cancel")
	}
}

func TestRunMachineSupervisorEnsuresWhenV2(t *testing.T) {
	h, rt, _, _, root := v2Host(t)
	runSupervisorBriefly(t, h, rt)
	if rt.launchCount() != 1 {
		t.Fatalf("launches = %d; want 1", rt.launchCount())
	}
	if rt.spec().RepoRoot != root {
		t.Errorf("supervisor launched %q; want %q", rt.spec().RepoRoot, root)
	}
}

func TestRunMachineSupervisorNoopWhenV1(t *testing.T) {
	defer ocv2.SetInstalledV2(false)()
	t.Setenv("HOME", t.TempDir())
	rt := &fakeRuntime{}
	h := New(Deps{Runtime: rt})
	runSupervisorBriefly(t, h, rt)
	if rt.launchCount() != 0 {
		t.Fatalf("launches = %d; want 0 on v1", rt.launchCount())
	}
}

// A machine server writes one database. After ocman restarts on another
// database (-db / OPENCODE_DB), the persisted server for the first one must
// not be reused: ocman would read B while the server keeps writing A.
func TestV2MachineServerIsBoundToItsDatabase(t *testing.T) {
	_, rt, store, _, defaultRoot := v2Host(t)
	ctx := context.Background()
	repo := initRepo(t)
	newHost := func() *Host { return New(Deps{Runtime: rt, ManagedStore: store}) }

	t.Setenv("OPENCODE_DB", "/data/a.db")
	if _, err := newHost().EnsureProjectOpencode(ctx, hostsvc.EnsureProjectOpencodeRequest{ProjectDir: repo}); err != nil {
		t.Fatal(err)
	}
	rootA := rt.spec().RepoRoot
	t.Setenv("OPENCODE_DB", "/data/b.db")
	if _, err := newHost().EnsureProjectOpencode(ctx, hostsvc.EnsureProjectOpencodeRequest{ProjectDir: repo}); err != nil {
		t.Fatal(err)
	}
	if rt.launchCount() != 2 {
		t.Fatalf("launches = %d, want a fresh server for database B", rt.launchCount())
	}
	if rootB := rt.spec().RepoRoot; rootA == rootB || rootA == defaultRoot || rootB == defaultRoot {
		t.Fatalf("roots A=%q B=%q default=%q, want one per database", rootA, rootB, defaultRoot)
	}
	// The same database maps to the same server again.
	t.Setenv("OPENCODE_DB", "/data/a.db")
	if _, err := newHost().EnsureProjectOpencode(ctx, hostsvc.EnsureProjectOpencodeRequest{ProjectDir: repo}); err != nil {
		t.Fatal(err)
	}
	if rt.launchCount() != 2 {
		t.Fatalf("launches = %d, want database A's server reused", rt.launchCount())
	}
}

// After an upgrade, v1 per-project rows (and v2 servers for another
// database) are stopped and forgotten, and only the machine server is
// reported, so a restart never hits the shared server once per stale row.
func TestV2SupervisorReapsNonMachineInstances(t *testing.T) {
	h, rt, store, _, root := v2Host(t)
	ctx := context.Background()
	_ = store.Upsert(ctx, "/src/old-v1-project", ManagedInstance{Endpoint: "http://127.0.0.1:9", RuntimeID: "old"})
	_ = store.Upsert(ctx, root+"-deadbeef", ManagedInstance{Endpoint: "http://127.0.0.1:8", RuntimeID: "otherdb"})

	inv, err := h.ManagedOpencodes(ctx)
	if err != nil || len(inv) != 0 {
		t.Fatalf("before the machine server exists: inventory = %+v, %v; stale rows must be hidden", inv, err)
	}
	runSupervisorBriefly(t, h, rt)
	if rt.stopCount() != 2 {
		t.Errorf("stops = %d, want both stale instances stopped", rt.stopCount())
	}
	rows, _ := store.List(ctx)
	if _, ok := rows["/src/old-v1-project"]; ok || len(rows) != 1 {
		t.Fatalf("rows after reap = %v, want only the machine server", rows)
	}
	inv, _ = h.ManagedOpencodes(ctx)
	if len(inv) != 1 || !inv[0].Machine || inv[0].RepoRoot != root {
		t.Fatalf("inventory = %+v, want the machine server only", inv)
	}
}
