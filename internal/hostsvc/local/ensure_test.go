package local

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/git"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/ocruntime"
)

// fakeRuntime is a test double for ocruntime.Runtime. It records launches
// and lets each test script Probe health via probe.
type fakeRuntime struct {
	mu       sync.Mutex
	launches int32
	stops    int32
	// stopErr, when set, is returned by Stop (to prove restart soft-fails).
	stopErr  error
	lastSpec ocruntime.LaunchSpec
	// endpoint is the endpoint reported by each launched Instance.
	endpoint string
	// launchErr, when set, is returned by Launch.
	launchErr error
	// probe decides health for a given instance; nil means "always
	// healthy once launched".
	probe func(inst *ocruntime.Instance) bool
	// launchDelay lets a test widen the window where a concurrent call
	// could sneak a second launch in (to prove singleflight collapses it).
	launchDelay time.Duration
	// launchEndpoint, when set, mints the endpoint for each launched
	// Instance (overrides endpoint). Lets a test give each launch a
	// distinct endpoint.
	launchEndpoint func() string
}

func (f *fakeRuntime) Launch(_ context.Context, spec ocruntime.LaunchSpec) (*ocruntime.Instance, error) {
	if f.launchDelay > 0 {
		time.Sleep(f.launchDelay)
	}
	atomic.AddInt32(&f.launches, 1)
	f.mu.Lock()
	f.lastSpec = spec
	f.mu.Unlock()
	if f.launchErr != nil {
		return nil, f.launchErr
	}
	ep := f.endpoint
	if f.launchEndpoint != nil {
		ep = f.launchEndpoint()
	}
	if ep == "" {
		ep = "http://127.0.0.1:6666"
	}
	return &ocruntime.Instance{Endpoint: ep, Kind: ocruntime.KindNativeTmux, ID: "sess-name"}, nil
}

func (f *fakeRuntime) Probe(_ context.Context, inst *ocruntime.Instance) error {
	if f.probe != nil {
		if f.probe(inst) {
			return nil
		}
		return ocruntime.ErrProbeUnreachable
	}
	return nil
}

func (f *fakeRuntime) Stop(_ context.Context, _ *ocruntime.Instance) error {
	atomic.AddInt32(&f.stops, 1)
	return f.stopErr
}

func (f *fakeRuntime) launchCount() int { return int(atomic.LoadInt32(&f.launches)) }
func (f *fakeRuntime) stopCount() int   { return int(atomic.LoadInt32(&f.stops)) }

func (f *fakeRuntime) spec() ocruntime.LaunchSpec {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastSpec
}

// TestEnsureProjectOpencode_LaunchesAndSeeds: no instance -> launches once,
// seeds OPENCODE_PERMISSION with a scoped external_directory rule, waits
// for the instance to serve, returns a reachable endpoint + instance.
func TestEnsureProjectOpencode_LaunchesAndSeeds(t *testing.T) {
	repo := initRepo(t)
	rt := &fakeRuntime{endpoint: "http://127.0.0.1:6666"}
	h := New(Deps{Runtime: rt})

	res, err := h.EnsureProjectOpencode(context.Background(), hostsvc.EnsureProjectOpencodeRequest{ProjectDir: repo})
	if err != nil {
		t.Fatalf("EnsureProjectOpencode: %v", err)
	}
	if rt.launchCount() != 1 {
		t.Errorf("launched %d times; want exactly 1", rt.launchCount())
	}
	if !res.Launched {
		t.Error("Launched should be true when this call started the instance")
	}
	if res.Endpoint != "http://127.0.0.1:6666" {
		t.Errorf("Endpoint = %q; want http://127.0.0.1:6666", res.Endpoint)
	}
	if res.Port() != "6666" {
		t.Errorf("Port() = %q; want 6666", res.Port())
	}
	if res.Runtime.ID != "sess-name" {
		t.Errorf("Runtime.ID = %q; want sess-name (tmux session for observability)", res.Runtime.ID)
	}
	if res.RepoRoot == "" {
		t.Error("RepoRoot should be resolved for observability")
	}
	spec := rt.spec()
	if spec.RepoRoot != res.RepoRoot {
		t.Errorf("launched RepoRoot %q != resolved repo root %q", spec.RepoRoot, res.RepoRoot)
	}
	if spec.Port <= 0 {
		t.Errorf("LaunchSpec.Port = %d; want a positive allocated port", spec.Port)
	}
	// The permission JSON must carry a scoped external_directory rule for
	// this project's .worktrees/<repo> root, not a blanket allow.
	if !strings.Contains(spec.PermissionJSON, "external_directory") {
		t.Errorf("permission JSON missing external_directory: %q", spec.PermissionJSON)
	}
	if !strings.Contains(spec.PermissionJSON, ".worktrees") {
		t.Errorf("permission JSON not scoped to .worktrees: %q", spec.PermissionJSON)
	}
	if strings.Contains(spec.PermissionJSON, `"*":"allow"`) {
		t.Errorf("permission JSON must not blanket-allow: %q", spec.PermissionJSON)
	}
}

func TestEnsureProjectOpencode_AdoptsDiscoveredServer(t *testing.T) {
	repo := initRepo(t)
	rt := &fakeRuntime{}
	h := New(Deps{
		Runtime:      rt,
		DiscoverPort: func(string) string { return "4096" },
	})

	res, err := h.EnsureProjectOpencode(context.Background(), hostsvc.EnsureProjectOpencodeRequest{ProjectDir: repo})
	if err != nil {
		t.Fatalf("EnsureProjectOpencode: %v", err)
	}
	if rt.launchCount() != 0 {
		t.Fatalf("launched %d times; want existing server reused", rt.launchCount())
	}
	if res.Endpoint != "http://127.0.0.1:4096" {
		t.Fatalf("Endpoint = %q, want discovered endpoint", res.Endpoint)
	}
	if res.Launched {
		t.Fatal("Launched should be false for a discovered server")
	}
}

// TestEnsureProjectOpencode_ReusesHealthyInstance: a second sequential call
// reuses the healthy instance the first launched — launches nothing more.
func TestEnsureProjectOpencode_ReusesHealthyInstance(t *testing.T) {
	repo := initRepo(t)
	rt := &fakeRuntime{} // Probe nil -> always healthy after launch.
	h := New(Deps{Runtime: rt})
	ctx := context.Background()

	first, err := h.EnsureProjectOpencode(ctx, hostsvc.EnsureProjectOpencodeRequest{ProjectDir: repo})
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if !first.Launched {
		t.Error("first call should report Launched")
	}
	second, err := h.EnsureProjectOpencode(ctx, hostsvc.EnsureProjectOpencodeRequest{ProjectDir: repo})
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if second.Launched {
		t.Error("second call should reuse the healthy instance, not launch")
	}
	if rt.launchCount() != 1 {
		t.Errorf("launched %d times; want exactly 1 across two calls", rt.launchCount())
	}
	if second.Endpoint != first.Endpoint {
		t.Errorf("reused endpoint = %q; want first endpoint %q", second.Endpoint, first.Endpoint)
	}
}

// TestEnsureProjectOpencode_Concurrent: overlapping ensure calls for one
// repo root launch at most one instance and all get the same endpoint.
func TestEnsureProjectOpencode_Concurrent(t *testing.T) {
	repo := initRepo(t)
	// A launch delay widens the race window; singleflight must still
	// collapse concurrent calls into one launch.
	rt := &fakeRuntime{launchDelay: 20 * time.Millisecond}
	h := New(Deps{Runtime: rt})
	ctx := context.Background()

	const n = 8
	var wg sync.WaitGroup
	endpoints := make([]string, n)
	errs := make([]error, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			res, err := h.EnsureProjectOpencode(ctx, hostsvc.EnsureProjectOpencodeRequest{ProjectDir: repo})
			errs[i] = err
			if res != nil {
				endpoints[i] = res.Endpoint
			}
		}(i)
	}
	wg.Wait()

	if rt.launchCount() != 1 {
		t.Errorf("launched %d times under concurrency; want exactly 1", rt.launchCount())
	}
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Errorf("call %d: %v", i, errs[i])
		}
		if endpoints[i] != endpoints[0] {
			t.Errorf("call %d endpoint = %q; want %q (all callers share one)", i, endpoints[i], endpoints[0])
		}
	}
}

// TestEnsureProjectOpencode_StaleRelaunch: a recorded instance that Probe
// reports dead does not suppress relaunch — the next ensure launches again.
// The probe reports healthy only for the endpoint from the latest launch,
// so between the two ensure calls the first instance becomes stale and the
// reuse check fails, forcing a relaunch. Deterministic: no timing races.
func TestEnsureProjectOpencode_StaleRelaunch(t *testing.T) {
	repo := initRepo(t)
	var launched int32
	rt := &fakeRuntime{}
	// Each launch mints a distinct endpoint (":7001", ":7002", ...).
	rt.launchEndpoint = func() string {
		n := atomic.AddInt32(&launched, 1)
		return "http://127.0.0.1:700" + string('0'+n)
	}
	// Only the endpoint minted by the most recent launch is healthy; any
	// earlier (recorded) instance probes dead.
	rt.probe = func(inst *ocruntime.Instance) bool {
		latest := "http://127.0.0.1:700" + string('0'+atomic.LoadInt32(&launched))
		return inst.Endpoint == latest
	}
	h := New(Deps{Runtime: rt})
	ctx := context.Background()

	// First call launches instance :7001 and records it. waitForProbe
	// passes because :7001 is the latest.
	first, err := h.EnsureProjectOpencode(ctx, hostsvc.EnsureProjectOpencodeRequest{ProjectDir: repo})
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if rt.launchCount() != 1 || first.Endpoint != "http://127.0.0.1:7001" {
		t.Fatalf("after first call: launches=%d endpoint=%q", rt.launchCount(), first.Endpoint)
	}

	// Simulate the recorded instance dying: bump the "latest" generation
	// so the reuse Probe on :7001 now returns false and forces a relaunch.
	atomic.AddInt32(&launched, 1) // now :7002 is "latest", :7001 is stale
	res, err := h.EnsureProjectOpencode(ctx, hostsvc.EnsureProjectOpencodeRequest{ProjectDir: repo})
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if !res.Launched {
		t.Error("a stale instance must trigger a relaunch (Launched=true)")
	}
	if rt.launchCount() != 2 {
		t.Errorf("launched %d times; want 2 (stale instance relaunched)", rt.launchCount())
	}
}

// TestEnsureProjectOpencode_NonRepo: a directory that is not a git repo
// returns git.ErrNotARepo and launches nothing.
func TestEnsureProjectOpencode_NonRepo(t *testing.T) {
	dir := t.TempDir() // not a git repo
	rt := &fakeRuntime{}
	h := New(Deps{Runtime: rt})
	_, err := h.EnsureProjectOpencode(context.Background(), hostsvc.EnsureProjectOpencodeRequest{ProjectDir: dir})
	if !errors.Is(err, git.ErrNotARepo) {
		t.Fatalf("err = %v; want git.ErrNotARepo", err)
	}
	if rt.launchCount() != 0 {
		t.Errorf("launched %d times for a non-repo; want 0", rt.launchCount())
	}
}

// TestEnsureProjectOpencode_HealthTimeout: launch succeeds but the instance
// never becomes healthy (Probe always false) -> a timeout error, launched
// exactly once.
func TestEnsureProjectOpencode_HealthTimeout(t *testing.T) {
	repo := initRepo(t)
	rt := &fakeRuntime{probe: func(*ocruntime.Instance) bool { return false }}
	h := New(Deps{Runtime: rt})
	h.portWaitTimeout = 30 * time.Millisecond
	h.portWaitInterval = 5 * time.Millisecond

	_, err := h.EnsureProjectOpencode(context.Background(), hostsvc.EnsureProjectOpencodeRequest{ProjectDir: repo})
	if err == nil {
		t.Fatal("expected a timeout error when the instance never becomes healthy")
	}
	if rt.launchCount() != 1 {
		t.Errorf("launched %d times; want exactly 1", rt.launchCount())
	}
}
