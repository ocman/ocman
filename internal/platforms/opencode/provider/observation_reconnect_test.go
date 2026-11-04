package opencode

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/ocman/ocman/internal/platforms/opencode/api"
)

func TestMaintenanceWaitsForReplacementPromptSnapshot(t *testing.T) {
	var reconnect, fail atomic.Bool
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if reconnect.Load() {
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		if fail.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/question" {
			_, _ = fmt.Fprint(w, `[{"id":"q","sessionID":"old-session"}]`)
		} else {
			_, _ = fmt.Fprint(w, `[]`)
		}
	}))
	defer upstream.Close()
	_, port, err := net.SplitHostPort(upstream.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	a := New(nil, nil, nil, ocapi.Auth{})
	a.rt.writeCachedPorts(map[string]string{"/repo": port})
	a.SeedSessionStatus(port, a.StatusPortGeneration(port), 0, nil)
	if !<-a.StartPromptReconciliation(t.Context(), port, []string{"/repo"}) {
		t.Fatal("initial reconciliation failed")
	}
	if _, err := a.SessionObservationsReady(t.Context()); err != nil {
		t.Fatal(err)
	}
	a.ClearPromptsForPort(port)
	reconnect.Store(true)
	fail.Store(true)
	// Scope ownership is registered synchronously, while both fetches wait.
	done := a.StartPromptReconciliation(t.Context(), port, []string{"/repo"})
	if _, err := a.SessionObservationsReady(t.Context()); err == nil {
		close(release)
		<-done
		t.Fatal("cleared prompt cache became ready before its replacement snapshot")
	}
	close(release)
	if <-done {
		t.Fatal("expected failed replacement snapshot")
	}
	if _, err := a.SessionObservationsReady(t.Context()); err == nil {
		t.Fatal("failed replacement marked prompts ready")
	}
	fail.Store(false)
	if !<-a.StartPromptReconciliation(t.Context(), port, []string{"/repo"}) {
		t.Fatal("replacement retry failed")
	}
	if _, err := a.SessionObservationsReady(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !a.CachedSessionObservations()["old-session"].PendingQuestion {
		t.Fatal("replacement lost pending question")
	}
}
