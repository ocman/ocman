package local

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/ocruntime"
)

// #456: the singleflight body must not run under the winning caller's
// context. If the winner's request is cancelled mid-launch, every
// coalesced waiter — whose own contexts are still live — would receive
// a spurious failure for a launch that could have succeeded.
func TestEnsureProjectOpencode_WinnerCancelDoesNotFailWaiters(t *testing.T) {
	repo := initRepo(t)

	var healthy atomic.Bool
	rt := &fakeRuntime{
		endpoint: "http://127.0.0.1:6666",
		probe: func(*ocruntime.Instance) bool {
			return healthy.Load()
		},
	}
	h := New(Deps{Runtime: rt})
	h.portWaitInterval = 5 * time.Millisecond
	h.portWaitTimeout = 5 * time.Second

	// Winner: starts the launch, then has its request cancelled while
	// waitForProbe is still polling.
	ctxA, cancelA := context.WithCancel(context.Background())
	aDone := make(chan error, 1)
	go func() {
		_, err := h.EnsureProjectOpencode(ctxA, hostsvc.EnsureProjectOpencodeRequest{ProjectDir: repo})
		aDone <- err
	}()

	// Wait until the launch has happened, so A is inside waitForProbe.
	deadline := time.Now().Add(2 * time.Second)
	for rt.launchCount() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("launch never started")
		}
		time.Sleep(time.Millisecond)
	}

	// Waiter: coalesces into A's in-flight launch with a live context.
	bDone := make(chan error, 1)
	var bRes *hostsvc.EnsureProjectOpencodeResult
	go func() {
		res, err := h.EnsureProjectOpencode(context.Background(), hostsvc.EnsureProjectOpencodeRequest{ProjectDir: repo})
		bRes = res
		bDone <- err
	}()
	// Give B a moment to join the same singleflight key.
	time.Sleep(20 * time.Millisecond)

	// The winner's request dies; then the instance becomes healthy.
	cancelA()
	time.Sleep(20 * time.Millisecond)
	healthy.Store(true)

	select {
	case err := <-bDone:
		if err != nil {
			t.Fatalf("waiter with a live context got %v; want success (winner's cancel must not poison the flight)", err)
		}
		if bRes == nil || bRes.Endpoint != "http://127.0.0.1:6666" {
			t.Fatalf("waiter result = %+v; want the launched endpoint", bRes)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("waiter never returned")
	}

	// The cancelled winner returns promptly with its own ctx error.
	select {
	case err := <-aDone:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("winner err = %v; want nil or context.Canceled", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("cancelled winner never returned")
	}

	if got := rt.launchCount(); got != 1 {
		t.Fatalf("launches = %d; want exactly 1 (singleflight)", got)
	}
}
