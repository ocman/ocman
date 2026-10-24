package local

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/NoUseFreak/ocman/internal/ocruntime"
	"github.com/NoUseFreak/ocman/internal/providerprotocol"
	"github.com/NoUseFreak/ocman/internal/providersupervisor"
	"github.com/NoUseFreak/ocman/internal/providersupervisor/providertest"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
)

func TestIdleCullRollbackAndInterruptedCleanupRecovery(t *testing.T) {
	for _, mode := range []string{"cancelled", "lost response", "failed rollback", "stop failure"} {
		t.Run(mode, func(t *testing.T) {
			dir := providertest.Directory(t)
			sup, err := providersupervisor.New(filepath.Join(dir, "spool"), providersupervisor.Backend{Hello: providerprotocol.Hello{
				PlatformID: "fake", ProtocolVersion: providerprotocol.CurrentVersion, CLIVersion: "test",
			}})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var failResume atomic.Bool
			failResume.Store(mode == "failed rollback")
			var resumes atomic.Int32
			server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/resume" {
					resumes.Add(1)
					if r.Context().Err() != nil {
						t.Error("rollback request was cancelled")
					}
					if failResume.Load() {
						w.WriteHeader(http.StatusInternalServerError)
						return
					}
				}
				if r.URL.Path == "/drain-idle" && mode != "stop failure" {
					sup.ServeHTTP(httptest.NewRecorder(), r) // Pause before losing the response.
					if mode == "cancelled" {
						cancel()
					}
					conn, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					_ = conn.Close()
					return
				}
				sup.ServeHTTP(w, r)
			})}
			socket := filepath.Join(dir, "s")
			listener, err := net.Listen("unix", socket)
			if err != nil {
				t.Fatal(err)
			}
			go func() { _ = server.Serve(listener) }()
			t.Cleanup(func() { _ = server.Close() })
			store := statetest.Open(t)
			runtime := ocruntime.NewBackendRuntime("fake", "/unused", dir)
			// An invalid cleanup handle makes Stop fail without invoking tmux.
			mustIdle(t, store.UpsertManagedProvider(t.Context(), "fake", "/repo", state.ManagedInstance{Endpoint: "unix://" + socket, Kind: ocruntime.KindProviderTmux, RuntimeID: "invalid/name"}))
			manager := &ProviderManager{Runtime: runtime, State: store}
			stopped, err := manager.CullIdle(ctx, "/repo", func(context.Context) (bool, error) { return true, nil }, func() { t.Error("detached a live supervisor") })
			if err == nil || stopped || resumes.Load() != 1 {
				t.Fatalf("stopped=%v err=%v resumes=%d", stopped, err, resumes.Load())
			}
			if mode == "failed rollback" {
				if err := sup.BeginTurn("blocked", nil); err == nil {
					t.Fatal("expected retained cleanup pause")
				}
				failResume.Store(false)
				// A fresh manager simulates dashboard restart: recovery comes from
				// /hello, not an in-memory token retained by the original manager.
				fresh := &ProviderManager{Runtime: runtime, State: store}
				result, err := fresh.ensureRoot(t.Context(), "/repo")
				if err != nil || result.Launched {
					t.Fatalf("recovery=%+v err=%v", result, err)
				}
			}
			if err := sup.BeginTurn("next", nil); err != nil {
				t.Fatalf("surviving supervisor stranded draining: %v", err)
			}
			if mode == "cancelled" && !errors.Is(ctx.Err(), context.Canceled) {
				t.Fatal("did not cancel original request")
			}
		})
	}
}

func mustIdle(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
