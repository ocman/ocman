package ocruntime

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestMissingManagedSessionIsGoneOnlyWhenEndpointIsGone(t *testing.T) {
	for _, alive := range []bool{false, true} {
		t.Run(map[bool]string{false: "absent", true: "API still alive"}[alive], func(t *testing.T) {
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
			defer api.Close()
			endpoint := api.URL
			if !alive {
				api.Close()
			}
			killErr := &exec.ExitError{Stderr: []byte("can't find session: owned")}
			runtime := &NativeRuntime{httpClient: api.Client(), kill: func(context.Context, string) error { return killErr }}
			err := runtime.Stop(t.Context(), &Instance{ID: "owned", Endpoint: endpoint})
			if alive && err == nil {
				t.Fatal("live endpoint was treated as stopped")
			}
			if !alive && err != nil {
				t.Fatalf("already absent instance blocked recovery: %v", err)
			}
		})
	}
}

func TestProductionKillRunnerPreservesMissingSessionDiagnostics(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	endpoint := api.URL
	api.Close()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte("#!/bin/sh\nprintf \"can't find session: owned\\n\" >&2\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	runtime := NewNativeRuntime()
	if err := runtime.Stop(t.Context(), &Instance{ID: "owned", Endpoint: endpoint}); err != nil {
		t.Fatalf("production runner discarded absence evidence: %v", err)
	}
}

func TestNativeStopCanEstablishAbsenceWithoutRuntimeID(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	endpoint := api.URL
	api.Close()
	runtime := NewNativeRuntime()
	if err := runtime.Stop(t.Context(), &Instance{Endpoint: endpoint}); err != nil {
		t.Fatalf("positively absent adopted server blocked recovery: %v", err)
	}
}

func TestMissingTmuxBinaryIsNotEvidenceOfStoppedSession(t *testing.T) {
	for _, err := range []error{errors.New("no server running"), &os.PathError{Op: "fork/exec", Path: "tmux", Err: syscall.ENOENT}, &exec.ExitError{Stderr: []byte("permission denied")}} {
		if missingNativeSession(err) {
			t.Fatalf("unproven failure treated as absent session: %T", err)
		}
	}
}

func TestProbeIdentityMismatchIsDistinguishable(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/project/current" {
			_, _ = w.Write([]byte(`{"worktree":"/foreign"}`))
		}
	}))
	defer api.Close()
	runtime := &NativeRuntime{httpClient: api.Client()}
	err := runtime.Probe(t.Context(), &Instance{Endpoint: api.URL, RepoRoot: "/owned"})
	if !errors.Is(err, ErrProbeIdentityMismatch) || !errors.Is(err, ErrProbeUnreachable) {
		t.Fatalf("identity rejection lost classification: %v", err)
	}
}

func TestSuccessfulKillWaitsForDelayedEndpointClosure(t *testing.T) {
	probed := make(chan struct{}, 8)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		select {
		case probed <- struct{}{}:
		default:
		}
	}))
	defer api.Close()
	var kills atomic.Int32
	runtime := &NativeRuntime{httpClient: api.Client(), kill: func(context.Context, string) error { kills.Add(1); return nil }}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runtime.Stop(ctx, &Instance{ID: "owned", Endpoint: api.URL}) }()
	// Keep the endpoint alive for multiple loop iterations after a successful kill.
	for probe := 0; probe < 2; probe++ {
		select {
		case <-probed:
		case err := <-done:
			t.Fatalf("Stop returned before endpoint closure: %v", err)
		case <-ctx.Done():
			t.Fatal("Stop never probed the still-serving endpoint")
		}
	}
	select {
	case err := <-done:
		t.Fatalf("successful kill was mistaken for endpoint closure: %v", err)
	default:
	}
	api.Close()
	select {
	case err := <-done:
		if err != nil || kills.Load() != 1 {
			t.Fatalf("delayed closure failed: err=%v kills=%d", err, kills.Load())
		}
	case <-ctx.Done():
		t.Fatal("Stop did not establish endpoint closure")
	}
}

func TestSuccessfulKillStillServingEndpointCannotConfirmStop(t *testing.T) {
	var probes, kills atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { probes.Add(1); w.WriteHeader(http.StatusOK) }))
	defer api.Close()
	runtime := &NativeRuntime{httpClient: api.Client(), kill: func(context.Context, string) error { kills.Add(1); return nil }}
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	err := runtime.Stop(ctx, &Instance{ID: "owned", Endpoint: api.URL})
	if !errors.Is(err, context.DeadlineExceeded) || probes.Load() == 0 || kills.Load() != 1 {
		t.Fatalf("still-serving endpoint confirmed stopped: err=%v probes=%d kills=%d", err, probes.Load(), kills.Load())
	}
}

func TestSuccessfulKillEndpointConfirmationHonorsCancellation(t *testing.T) {
	probed := make(chan struct{}, 1)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		select {
		case probed <- struct{}{}:
		default:
		}
	}))
	defer api.Close()
	var kills atomic.Int32
	runtime := &NativeRuntime{httpClient: api.Client(), kill: func(context.Context, string) error { kills.Add(1); return nil }}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runtime.Stop(ctx, &Instance{ID: "owned", Endpoint: api.URL}) }()
	select {
	case <-probed:
	case err := <-done:
		t.Fatalf("Stop returned before cancellation: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("Stop never reached endpoint confirmation")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || kills.Load() != 1 {
			t.Fatalf("cancelled confirmation claimed success: err=%v kills=%d", err, kills.Load())
		}
	case <-time.After(time.Second):
		t.Fatal("Stop did not honor confirmation cancellation")
	}
}
