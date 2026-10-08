package local

import (
	"context"
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/ocruntime"
)

func TestRejectedCleanupCandidateCannotRegainRoutingOnInconclusiveRetry(t *testing.T) {
	for _, restart := range []bool{false, true} {
		t.Run(map[bool]string{false: "ensure", true: "restart"}[restart], func(t *testing.T) {
			h, _, store, rec, root := v2Host(t)
			if err := store.Upsert(t.Context(), root, ManagedInstance{Endpoint: "http://127.0.0.1:7777", RuntimeID: "cleanup"}); err != nil {
				t.Fatal(err)
			}
			rt := &probeErrorRuntime{err: ocruntime.ErrProbeIdentityMismatch}
			h.runtime = rt
			prepareErr := errors.New("preparation failed")
			h.deps.BeforeReplace = func(context.Context, string, string) error { return prepareErr }
			if _, err := h.EnsureProjectOpencode(t.Context(), hostsvc.EnsureProjectOpencodeRequest{}); !errors.Is(err, prepareErr) {
				t.Fatal(err)
			}
			rt.err = context.DeadlineExceeded
			var err error
			if restart {
				_, err = h.RestartProjectOpencode(t.Context(), hostsvc.EnsureProjectOpencodeRequest{})
			} else {
				_, err = h.EnsureProjectOpencode(t.Context(), hostsvc.EnsureProjectOpencodeRequest{})
			}
			if err == nil || rec.last() != "" || !store.has(root) || rt.stopCount() != 0 || rt.launchCount() != 0 {
				t.Fatalf("rejected cleanup regained authorization: err=%v port=%s durable=%t", err, rec.last(), store.has(root))
			}
		})
	}
}

func TestExplicitRestartAmbiguousColdCandidateIsCleanupOnly(t *testing.T) {
	h, _, store, rec, root := v2Host(t)
	if err := store.Upsert(t.Context(), root, ManagedInstance{Endpoint: "http://127.0.0.1:7777", RuntimeID: "cleanup"}); err != nil {
		t.Fatal(err)
	}
	h.runtime = &probeErrorRuntime{err: context.DeadlineExceeded}
	prepareErr := errors.New("preparation failed")
	h.deps.BeforeReplace = func(context.Context, string, string) error { return prepareErr }
	if _, err := h.RestartProjectOpencode(t.Context(), hostsvc.EnsureProjectOpencodeRequest{}); !errors.Is(err, prepareErr) {
		t.Fatal(err)
	}
	if rec.last() != "" || !store.has(root) {
		t.Fatalf("unvalidated restart candidate published: port=%s", rec.last())
	}
}

func TestStoppedUnconfirmedCandidateIsNotRepublishedOnInconclusiveRetry(t *testing.T) {
	h, _, store, rec, root := v2Host(t)
	rt := &probeErrorRuntime{}
	h.runtime = rt
	if _, err := h.EnsureProjectOpencode(t.Context(), hostsvc.EnsureProjectOpencodeRequest{}); err != nil {
		t.Fatal(err)
	}
	confirmErr := errors.New("confirmation failed")
	h.deps.AfterStop = func(context.Context, string) error { return confirmErr }
	if _, err := h.RestartProjectOpencode(t.Context(), hostsvc.EnsureProjectOpencodeRequest{}); !errors.Is(err, confirmErr) {
		t.Fatal(err)
	}
	if !store.has(root) || rec.last() != "" {
		t.Fatal("closure did not retain cleanup and clear routing")
	}
	rt.err = context.DeadlineExceeded
	if _, err := h.EnsureProjectOpencode(t.Context(), hostsvc.EnsureProjectOpencodeRequest{}); !errors.Is(err, confirmErr) {
		t.Fatal(err)
	}
	if rec.last() != "" {
		t.Fatalf("stopped endpoint republished: %s", rec.last())
	}
}

func TestColdCandidateSuccessfulValidationAuthorizesRouting(t *testing.T) {
	h, rt, store, rec, root := v2Host(t)
	if err := store.Upsert(t.Context(), root, ManagedInstance{Endpoint: "http://127.0.0.1:7777", RuntimeID: "owned"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.EnsureProjectOpencode(t.Context(), hostsvc.EnsureProjectOpencodeRequest{}); err != nil {
		t.Fatal(err)
	}
	if rec.last() != "7777" || rt.launchCount() != 0 || rt.stopCount() != 0 {
		t.Fatal("validated cold server not reused")
	}
	// Persisted handles do not transfer the old process's routing proof.
	cold := New(Deps{Runtime: &probeErrorRuntime{err: context.DeadlineExceeded}, ManagedStore: store, SetMachineServer: rec.set})
	if _, err := cold.EnsureProjectOpencode(t.Context(), hostsvc.EnsureProjectOpencodeRequest{}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if rec.last() != "" {
		t.Fatal("new owner process trusted durable routing authorization")
	}
}

func TestFailedMachineStopPreservesPreviouslyAuthorizedRouting(t *testing.T) {
	h, rt, _, rec, root := v2Host(t)
	if _, err := h.EnsureProjectOpencode(t.Context(), hostsvc.EnsureProjectOpencodeRequest{}); err != nil {
		t.Fatal(err)
	}
	rt.stopErr = errors.New("stop failed")
	if err := h.StopProjectOpencode(t.Context(), hostsvc.EnsureProjectOpencodeRequest{ProjectDir: root}); !errors.Is(err, rt.stopErr) {
		t.Fatal(err)
	}
	if rec.last() != "7777" {
		t.Fatal("failed Stop withdrew previously validated routing")
	}
}
