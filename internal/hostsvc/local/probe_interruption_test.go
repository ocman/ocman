package local

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/ocruntime"
	"github.com/NoUseFreak/ocman/internal/ocv2"
)

type probeErrorRuntime struct {
	fakeRuntime
	err error
}

func TestV2InconclusiveProbeKeepsPublishedServer(t *testing.T) {
	t.Cleanup(ocv2.SetInstalledV2(true))
	t.Setenv("HOME", t.TempDir())
	t.Setenv("OPENCODE_DB", "")
	for _, probeErr := range []error{context.DeadlineExceeded, context.Canceled, ocruntime.ErrProbeNotReady} {
		rt := &probeErrorRuntime{}
		rec := &portRecorder{}
		h := New(Deps{Runtime: rt, SetMachineServer: rec.set})
		if _, err := h.EnsureProjectOpencode(t.Context(), hostsvc.EnsureProjectOpencodeRequest{}); err != nil {
			t.Fatal(err)
		}
		port := rec.last()
		rt.err = probeErr
		_, err := h.EnsureProjectOpencode(t.Context(), hostsvc.EnsureProjectOpencodeRequest{})
		if !errors.Is(err, probeErr) || rec.last() != port || rt.stopCount() != 0 || rt.launchCount() != 1 {
			t.Fatalf("live v2 routing lost: err=%v published=%s want=%s stops=%d launches=%d", err, rec.last(), port, rt.stopCount(), rt.launchCount())
		}
	}
}

func TestV2InconclusiveDiscoveredProbeRetainsCleanupOnly(t *testing.T) {
	t.Cleanup(ocv2.SetInstalledV2(true))
	t.Setenv("HOME", t.TempDir())
	t.Setenv("OPENCODE_DB", "")
	rt := &probeErrorRuntime{err: ocruntime.ErrProbeNotReady}
	rec := &portRecorder{}
	rec.set("6666")
	h := New(Deps{Runtime: rt, SetMachineServer: rec.set, DiscoverPort: func(string) string { return "6666" }})
	_, err := h.EnsureProjectOpencode(t.Context(), hostsvc.EnsureProjectOpencodeRequest{})
	if !errors.Is(err, ocruntime.ErrProbeNotReady) || rec.last() != "" || h.currentInstance(machineRoot()) == nil || rt.launchCount() != 0 {
		t.Fatalf("discovered v2 server lost: %v port=%s launches=%d", err, rec.last(), rt.launchCount())
	}
}

func TestV2ColdRecoveryDoesNotAuthorizeInconclusiveCandidate(t *testing.T) {
	h, _, store, rec, root := v2Host(t)
	if err := store.Upsert(t.Context(), root, ManagedInstance{Endpoint: "http://127.0.0.1:7777", Kind: ocruntime.KindNativeTmux, RuntimeID: "persisted"}); err != nil {
		t.Fatal(err)
	}
	rt := &probeErrorRuntime{err: context.DeadlineExceeded}
	h.runtime = rt
	_, err := h.EnsureProjectOpencode(t.Context(), hostsvc.EnsureProjectOpencodeRequest{})
	if !errors.Is(err, context.DeadlineExceeded) || rec.last() != "" || rt.stopCount() != 0 || rt.launchCount() != 0 {
		t.Fatalf("cold cleanup candidate authorized: %v port=%s stops=%d launches=%d", err, rec.last(), rt.stopCount(), rt.launchCount())
	}
}

func TestV2ColdPreparationFailureKeepsPersistedCandidate(t *testing.T) {
	for _, restart := range []bool{false, true} {
		t.Run(map[bool]string{false: "failed probe", true: "requested restart"}[restart], func(t *testing.T) {
			h, rt, store, rec, root := v2Host(t)
			if err := store.Upsert(t.Context(), root, ManagedInstance{Endpoint: "http://127.0.0.1:7777", Kind: ocruntime.KindNativeTmux, RuntimeID: "persisted"}); err != nil {
				t.Fatal(err)
			}
			prepareErr := errors.New("interruption preparation unavailable")
			h.deps.BeforeReplace = func(context.Context, string, string) error { return prepareErr }
			rt.probe = func(*ocruntime.Instance) bool { return false }
			var err error
			if restart {
				_, err = h.RestartProjectOpencode(t.Context(), hostsvc.EnsureProjectOpencodeRequest{})
			} else {
				_, err = h.EnsureProjectOpencode(t.Context(), hostsvc.EnsureProjectOpencodeRequest{})
			}
			if !errors.Is(err, prepareErr) || rec.last() != "" || h.currentInstance(root) == nil || rt.stopCount() != 0 || rt.launchCount() != 0 {
				t.Fatalf("cold preparation authorized retained server: %v port=%s stops=%d launches=%d", err, rec.last(), rt.stopCount(), rt.launchCount())
			}
		})
	}
}

func TestV2IdentityRejectedCandidateIsNotPublishedAfterPreparationFailure(t *testing.T) {
	h, _, store, rec, root := v2Host(t)
	if err := store.Upsert(t.Context(), root, ManagedInstance{Endpoint: "http://127.0.0.1:7777", Kind: ocruntime.KindNativeTmux, RuntimeID: "old-owned-handle"}); err != nil {
		t.Fatal(err)
	}
	rec.set("7777")
	rt := &probeErrorRuntime{err: ocruntime.ErrProbeIdentityMismatch}
	h.runtime = rt
	prepareErr := errors.New("preparation failed")
	h.deps.BeforeReplace = func(context.Context, string, string) error { return prepareErr }
	_, err := h.EnsureProjectOpencode(t.Context(), hostsvc.EnsureProjectOpencodeRequest{})
	if !errors.Is(err, prepareErr) || rec.last() != "" || rt.stopCount() != 0 || rt.launchCount() != 0 {
		t.Fatalf("rejected endpoint authorized: err=%v published=%s", err, rec.last())
	}
}

func TestExplicitRestartDoesNotRepublishPreviouslyRejectedCandidate(t *testing.T) {
	h, _, store, rec, root := v2Host(t)
	if err := store.Upsert(t.Context(), root, ManagedInstance{Endpoint: "http://127.0.0.1:7777", Kind: ocruntime.KindNativeTmux, RuntimeID: "cleanup"}); err != nil {
		t.Fatal(err)
	}
	h.runtime = &probeErrorRuntime{err: ocruntime.ErrProbeIdentityMismatch}
	prepareErr := errors.New("preparation failed")
	h.deps.BeforeReplace = func(context.Context, string, string) error { return prepareErr }
	if _, err := h.EnsureProjectOpencode(t.Context(), hostsvc.EnsureProjectOpencodeRequest{}); !errors.Is(err, prepareErr) {
		t.Fatal(err)
	}
	if rec.last() != "" {
		t.Fatal("initial rejection left routing published")
	}
	if _, err := h.RestartProjectOpencode(t.Context(), hostsvc.EnsureProjectOpencodeRequest{}); !errors.Is(err, prepareErr) {
		t.Fatal(err)
	}
	if rec.last() != "" {
		t.Fatal("explicit restart republished the cleanup-only endpoint")
	}
}

func TestV2AbortedReplacementKeepsPublishedServer(t *testing.T) {
	t.Cleanup(ocv2.SetInstalledV2(true))
	t.Setenv("HOME", t.TempDir())
	t.Setenv("OPENCODE_DB", "")
	rt := &probeErrorRuntime{}
	rec := &portRecorder{}
	prepareErr := errors.New("cannot persist interruptions")
	h := New(Deps{Runtime: rt, SetMachineServer: rec.set, BeforeReplace: func(context.Context, string, string) error { return prepareErr }})
	if _, err := h.EnsureProjectOpencode(t.Context(), hostsvc.EnsureProjectOpencodeRequest{}); err != nil {
		t.Fatal(err)
	}
	port := rec.last()
	rt.err = ocruntime.ErrProbeUnreachable
	_, err := h.EnsureProjectOpencode(t.Context(), hostsvc.EnsureProjectOpencodeRequest{})
	if !errors.Is(err, prepareErr) || rec.last() != port || rt.stopCount() != 0 || rt.launchCount() != 1 {
		t.Fatalf("aborted replacement lost v2 routing: %v port=%s stops=%d launches=%d", err, rec.last(), rt.stopCount(), rt.launchCount())
	}
}

func TestV2FailedReplacementWithdrawsStoppedEndpoint(t *testing.T) {
	h, rt, _, rec, _ := v2Host(t)
	if _, err := h.EnsureProjectOpencode(t.Context(), hostsvc.EnsureProjectOpencodeRequest{}); err != nil {
		t.Fatal(err)
	}
	rt.launchErr = context.DeadlineExceeded
	_, err := h.RestartProjectOpencode(t.Context(), hostsvc.EnsureProjectOpencodeRequest{})
	if !errors.Is(err, context.DeadlineExceeded) || rt.stopCount() != 1 || rec.last() != "" {
		t.Fatalf("stopped v2 endpoint still published: err=%v stops=%d port=%s", err, rt.stopCount(), rec.last())
	}
}

func TestV2DetachedReplacementPublishesAfterCallerCancellation(t *testing.T) {
	h, rt, _, rec, root := v2Host(t)
	if _, err := h.EnsureProjectOpencode(t.Context(), hostsvc.EnsureProjectOpencodeRequest{}); err != nil {
		t.Fatal(err)
	}
	rt.launchEndpoint = func() string { return "http://127.0.0.1:8888" }
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	h.runtime = &gatedLaunchRuntime{fakeRuntime: rt, started: started, release: release}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := h.RestartProjectOpencode(ctx, hostsvc.EnsureProjectOpencodeRequest{}); done <- err }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("replacement never reached launch gate")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("caller cancellation = %v", err)
	}
	// The singleflight body owns publication, so a cancelled waiter cannot
	// strand a successful replacement behind the old port.
	unblock()
	if _, err := h.EnsureProjectOpencode(t.Context(), hostsvc.EnsureProjectOpencodeRequest{}); err != nil {
		t.Fatal(err)
	}
	if rec.last() != "8888" || h.currentInstance(root) == nil || rt.launchCount() != 2 {
		t.Fatalf("detached replacement not published: port=%s", rec.last())
	}
}

type gatedLaunchRuntime struct {
	*fakeRuntime
	started chan struct{}
	release chan struct{}
}

func (r *gatedLaunchRuntime) Launch(ctx context.Context, spec ocruntime.LaunchSpec) (*ocruntime.Instance, error) {
	close(r.started)
	select {
	case <-r.release:
		return r.fakeRuntime.Launch(ctx, spec)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (r *probeErrorRuntime) Probe(context.Context, *ocruntime.Instance) error { return r.err }

func TestEnsureDoesNotKillServerOnAmbiguousProbeFailure(t *testing.T) {
	for _, probeErr := range []error{
		&net.DNSError{Err: "timeout", IsTimeout: true},
		context.Canceled,
		context.DeadlineExceeded,
		ocruntime.ErrProbeNotReady,
	} {
		t.Run(probeErr.Error(), func(t *testing.T) {
			repo := initRepo(t)
			repo, err := filepath.EvalSymlinks(repo)
			if err != nil {
				t.Fatal(err)
			}
			rt := &probeErrorRuntime{err: probeErr}
			h := New(Deps{Runtime: rt})
			h.portWaitTimeout = time.Millisecond
			inst := &ocruntime.Instance{Endpoint: "http://127.0.0.1:6666", ID: "shared"}
			h.setInstance(repo, inst)
			_, err = h.EnsureProjectOpencode(t.Context(), hostsvc.EnsureProjectOpencodeRequest{ProjectDir: repo})
			if !errors.Is(err, probeErr) || rt.stopCount() != 0 || rt.launchCount() != 0 || h.currentInstance(repo) != inst {
				t.Fatalf("ambiguous probe must preserve server: err=%v stops=%d launches=%d retained=%v", err, rt.stopCount(), rt.launchCount(), h.currentInstance(repo) == inst)
			}
		})
	}
}

func TestReplacementPersistsBeforeStoppingSharedServer(t *testing.T) {
	for _, requested := range []bool{false, true} {
		t.Run(map[bool]string{false: "health failure", true: "requested restart"}[requested], func(t *testing.T) {
			repo := initRepo(t)
			rt := &fakeRuntime{}
			called := 0
			confirmed := 0
			h := New(Deps{Runtime: rt, BeforeReplace: func(_ context.Context, root, reason string) error {
				called++
				if root != repo || reason == "" || rt.stopCount() != 0 {
					t.Fatalf("hook after stop or wrong scope: %s %s stops=%d", root, reason, rt.stopCount())
				}
				return nil
			}})
			h.deps.AfterStop = func(_ context.Context, root string) error {
				confirmed++
				if root != repo || rt.stopCount() != 1 || h.currentInstance(repo) != nil {
					t.Fatal("history confirmed before established stop")
				}
				return nil
			}
			h.setInstance(repo, &ocruntime.Instance{Endpoint: "old", ID: "shared"})
			rt.probe = func(inst *ocruntime.Instance) bool { return inst.Endpoint != "old" }
			var err error
			if requested {
				_, err = h.restartLocked(t.Context(), repo)
			} else {
				_, err = h.ensureLocked(t.Context(), repo)
			}
			if err != nil || called != 1 || confirmed != 1 || rt.stopCount() != 1 || rt.launchCount() != 1 {
				t.Fatalf("replacement: err=%v hooks=%d stops=%d launches=%d", err, called, rt.stopCount(), rt.launchCount())
			}
		})
	}
}

func TestStopFailureDoesNotConfirmOrLaunchReplacement(t *testing.T) {
	rt := &fakeRuntime{stopErr: errors.New("stop denied")}
	confirmed := 0
	h := New(Deps{Runtime: rt, AfterStop: func(context.Context, string) error { confirmed++; return nil }})
	inst := &ocruntime.Instance{ID: "owned", Endpoint: "old"}
	h.setInstance("repo", inst)
	_, err := h.restartLocked(t.Context(), "repo")
	if !errors.Is(err, rt.stopErr) || confirmed != 0 || rt.launchCount() != 0 || h.currentInstance("repo") != inst {
		t.Fatalf("failed Stop lost live instance or confirmed history: %v confirmed=%d launches=%d", err, confirmed, rt.launchCount())
	}
}

func TestFailedConfirmationPreservesDurableRecoveryHandle(t *testing.T) {
	store := newFakeStore()
	rt := &fakeRuntime{probe: func(inst *ocruntime.Instance) bool { return inst.Endpoint != "old" }}
	confirmErr := errors.New("confirmation unavailable")
	h := New(Deps{Runtime: rt, ManagedStore: store, AfterStop: func(context.Context, string) error { return confirmErr }})
	if err := store.Upsert(t.Context(), "repo", ManagedInstance{Endpoint: "old", RuntimeID: "owned", Kind: ocruntime.KindNativeTmux}); err != nil {
		t.Fatal(err)
	}
	_, err := h.restartLocked(t.Context(), "repo")
	if !errors.Is(err, confirmErr) || !store.has("repo") || h.currentInstance("repo") != nil || rt.launchCount() != 0 {
		t.Fatalf("stopped-but-unconfirmed recovery lost: err=%v durable=%v launches=%d", err, store.has("repo"), rt.launchCount())
	}
	// A fresh owner can repeat the idempotent stop and retry confirmation
	// before deleting the recovery handle or launching another server.
	recovered := New(Deps{Runtime: rt, ManagedStore: store, AfterStop: func(context.Context, string) error { return nil }})
	if _, err := recovered.ensureLocked(t.Context(), "repo"); err != nil {
		t.Fatal(err)
	}
	if rt.launchCount() != 1 || rt.stopCount() != 2 {
		t.Fatalf("recovery ordering lost: launches=%d stops=%d", rt.launchCount(), rt.stopCount())
	}
}

func TestFailedInterruptionWritePreventsReplacement(t *testing.T) {
	for _, requested := range []bool{false, true} {
		rt := &fakeRuntime{probe: func(*ocruntime.Instance) bool { return false }}
		writeErr := errors.New("state database unavailable")
		h := New(Deps{Runtime: rt, BeforeReplace: func(context.Context, string, string) error { return writeErr }})
		inst := &ocruntime.Instance{Endpoint: "old", ID: "shared"}
		h.setInstance("repo", inst)
		var err error
		if requested {
			_, err = h.restartLocked(t.Context(), "repo")
		} else {
			_, err = h.ensureLocked(t.Context(), "repo")
		}
		if !errors.Is(err, writeErr) || rt.stopCount() != 0 || rt.launchCount() != 0 || h.currentInstance("repo") != inst {
			t.Fatalf("lost server after failed notice: err=%v stops=%d launches=%d", err, rt.stopCount(), rt.launchCount())
		}
	}
}

func TestDiscoveredSlowServerIsNotReplaced(t *testing.T) {
	rt := &probeErrorRuntime{err: context.DeadlineExceeded}
	h := New(Deps{Runtime: rt, DiscoverPort: func(string) string { return "6666" }})
	_, err := h.ensureLocked(t.Context(), "repo")
	if !errors.Is(err, context.DeadlineExceeded) || rt.launchCount() != 0 {
		t.Fatalf("launched over slow discovered server: %v launches=%d", err, rt.launchCount())
	}
}
