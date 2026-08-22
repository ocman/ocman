package local

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/git"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/ocruntime"
)

// TestRestartProjectOpencode_StopsAndRelaunches: a restart with a tracked
// instance stops it exactly once, clears the cache, and launches a fresh
// one, returning the new endpoint with Launched=true.
func TestRestartProjectOpencode_StopsAndRelaunches(t *testing.T) {
	repo := initRepo(t)
	var launched int32
	rt := &fakeRuntime{}
	rt.launchEndpoint = func() string {
		n := atomic.AddInt32(&launched, 1)
		return "http://127.0.0.1:800" + string('0'+n)
	}
	// Everything the runtime just launched probes healthy.
	rt.probe = func(inst *ocruntime.Instance) bool {
		latest := "http://127.0.0.1:800" + string('0'+atomic.LoadInt32(&launched))
		return inst.Endpoint == latest
	}
	h := New(Deps{Runtime: rt})
	ctx := context.Background()

	// Ensure once to establish a tracked instance (:8001).
	first, err := h.EnsureProjectOpencode(ctx, hostsvc.EnsureProjectOpencodeRequest{ProjectDir: repo})
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if first.Endpoint != "http://127.0.0.1:8001" {
		t.Fatalf("first endpoint = %q", first.Endpoint)
	}

	// Restart: Stop the tracked instance once, relaunch (:8002).
	res, err := h.RestartProjectOpencode(ctx, hostsvc.EnsureProjectOpencodeRequest{ProjectDir: repo})
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	if rt.stopCount() != 1 {
		t.Errorf("Stop called %d times; want exactly 1", rt.stopCount())
	}
	if rt.launchCount() != 2 {
		t.Errorf("launched %d times; want 2 (ensure + restart)", rt.launchCount())
	}
	if !res.Launched {
		t.Error("restart result should report Launched=true")
	}
	if res.Endpoint != "http://127.0.0.1:8002" {
		t.Errorf("restart endpoint = %q; want the fresh :8002", res.Endpoint)
	}
}

// TestRestartProjectOpencode_NoTrackedInstance: a restart with nothing
// tracked still launches a healthy instance and does not call Stop.
func TestRestartProjectOpencode_NoTrackedInstance(t *testing.T) {
	repo := initRepo(t)
	rt := &fakeRuntime{endpoint: "http://127.0.0.1:6666"}
	h := New(Deps{Runtime: rt})

	res, err := h.RestartProjectOpencode(context.Background(), hostsvc.EnsureProjectOpencodeRequest{ProjectDir: repo})
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	if rt.stopCount() != 0 {
		t.Errorf("Stop called %d times; want 0 with nothing tracked", rt.stopCount())
	}
	if rt.launchCount() != 1 {
		t.Errorf("launched %d times; want 1", rt.launchCount())
	}
	if !res.Launched || res.Endpoint != "http://127.0.0.1:6666" {
		t.Errorf("result = %+v; want a healthy launched instance", res)
	}
}

// TestRestartProjectOpencode_SoftFailsOnStopError: a Stop error must not
// block the relaunch — the restart still returns a fresh healthy instance.
func TestRestartProjectOpencode_SoftFailsOnStopError(t *testing.T) {
	repo := initRepo(t)
	var launched int32
	rt := &fakeRuntime{stopErr: errors.New("boom")}
	rt.launchEndpoint = func() string {
		n := atomic.AddInt32(&launched, 1)
		return "http://127.0.0.1:900" + string('0'+n)
	}
	rt.probe = func(inst *ocruntime.Instance) bool {
		latest := "http://127.0.0.1:900" + string('0'+atomic.LoadInt32(&launched))
		return inst.Endpoint == latest
	}
	h := New(Deps{Runtime: rt})
	ctx := context.Background()

	if _, err := h.EnsureProjectOpencode(ctx, hostsvc.EnsureProjectOpencodeRequest{ProjectDir: repo}); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	res, err := h.RestartProjectOpencode(ctx, hostsvc.EnsureProjectOpencodeRequest{ProjectDir: repo})
	if err != nil {
		t.Fatalf("restart should soft-fail on Stop error, got: %v", err)
	}
	if rt.stopCount() != 1 {
		t.Errorf("Stop called %d times; want 1", rt.stopCount())
	}
	if !res.Launched || res.Endpoint != "http://127.0.0.1:9002" {
		t.Errorf("result = %+v; want the fresh relaunched instance", res)
	}
}

// TestRestartProjectOpencode_ClearsStoreRow: after a restart the persisted
// store holds the NEW row (fresh endpoint), not the old one, and the old
// row was deleted along the way.
func TestRestartProjectOpencode_ClearsStoreRow(t *testing.T) {
	repo := initRepo(t)
	ctx := context.Background()
	repoRoot, err := git.ResolveRepoRoot(ctx, repo)
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	var launched int32
	rt := &fakeRuntime{}
	rt.launchEndpoint = func() string {
		n := atomic.AddInt32(&launched, 1)
		return "http://127.0.0.1:850" + string('0'+n)
	}
	rt.probe = func(inst *ocruntime.Instance) bool {
		latest := "http://127.0.0.1:850" + string('0'+atomic.LoadInt32(&launched))
		return inst.Endpoint == latest
	}
	store := newFakeStore()
	h := New(Deps{Runtime: rt, ManagedStore: store})
	h.portWaitTimeout = time.Second
	h.portWaitInterval = 5 * time.Millisecond

	// Ensure once: store now holds :8501.
	if _, err := h.EnsureProjectOpencode(ctx, hostsvc.EnsureProjectOpencodeRequest{ProjectDir: repo}); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if row, ok, _ := store.Get(t.Context(), repoRoot); !ok || row.Endpoint != "http://127.0.0.1:8501" {
		t.Fatalf("after ensure store row = %+v (ok=%v); want :8501", row, ok)
	}

	// Restart: the old row must be deleted and a fresh one (:8502) upserted.
	res, err := h.RestartProjectOpencode(ctx, hostsvc.EnsureProjectOpencodeRequest{ProjectDir: repo})
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	if store.deletes != 1 {
		t.Errorf("store deletes = %d; want 1 (old row cleared)", store.deletes)
	}
	row, ok, _ := store.Get(t.Context(), repoRoot)
	if !ok {
		t.Fatal("store should hold the fresh row after restart")
	}
	if row.Endpoint != "http://127.0.0.1:8502" || row.Endpoint != res.Endpoint {
		t.Errorf("store row endpoint = %q; want the NEW endpoint %q", row.Endpoint, res.Endpoint)
	}
}

func TestStopProjectOpencode_StopsAndForgetsInstance(t *testing.T) {
	repo := initRepo(t)
	rt := &fakeRuntime{endpoint: "http://127.0.0.1:8111"}
	store := newFakeStore()
	h := New(Deps{Runtime: rt, ManagedStore: store})
	ctx := context.Background()

	if _, err := h.EnsureProjectOpencode(ctx, hostsvc.EnsureProjectOpencodeRequest{ProjectDir: repo}); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if err := h.StopProjectOpencode(ctx, hostsvc.EnsureProjectOpencodeRequest{ProjectDir: repo}); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if rt.stopCount() != 1 {
		t.Fatalf("Stop called %d times; want 1", rt.stopCount())
	}
	if err := h.StopProjectOpencode(ctx, hostsvc.EnsureProjectOpencodeRequest{ProjectDir: repo}); err != nil {
		t.Fatalf("second stop: %v", err)
	}
	if rt.stopCount() != 1 {
		t.Fatalf("second stop called runtime again; count = %d", rt.stopCount())
	}
}
