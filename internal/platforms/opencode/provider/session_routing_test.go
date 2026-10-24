package opencode

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/opencode/api"
)

func TestSessionIdentityOverridesDirectoryForStreamsAndMutations(t *testing.T) {
	const id, directory = "session-moved-project", "/no-longer-the-owner"
	var wrongRequests, sends, streams atomic.Int32
	wrong := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		wrongRequests.Add(1)
		http.NotFound(w, nil)
	}))
	t.Cleanup(wrong.Close)
	right := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/session/" + id:
			fmt.Fprintf(w, `{"id":%q}`, id)
		case "/event":
			streams.Add(1)
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "data: {\"type\":\"message.updated\",\"properties\":{\"sessionID\":%q}}\n\n", id)
		case "/session/" + id + "/prompt_async":
			sends.Add(1)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(right.Close)
	wrongPort := strings.TrimPrefix(wrong.URL, "http://127.0.0.1:")
	rightPort := strings.TrimPrefix(right.URL, "http://127.0.0.1:")
	restorePorts := setDiscoverPortsImplForTests(func() map[string]string { return map[string]string{directory: wrongPort} })
	restoreServers := setDiscoverServersImplForTests(func() []openCodeServer {
		return []openCodeServer{{directory: "/actual-owner", port: rightPort}}
	})
	t.Cleanup(restorePorts)
	t.Cleanup(restoreServers)
	resetPortCacheForTests()
	resetSessionPortAffinityForTests()
	t.Cleanup(resetPortCacheForTests)
	t.Cleanup(resetSessionPortAffinityForTests)
	a := New(newTestDBWithSession(t, id, directory), nil, nil, ocapi.Auth{})
	var out bytes.Buffer
	if err := a.ProxyEvents(context.Background(), id, &out, func() {}); err != nil {
		t.Fatalf("stream followed the directory instead of the native ID: %v", err)
	}
	if err := a.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: id, Message: "test"}); err != nil {
		t.Fatalf("send used a different route from SSE: %v", err)
	}
	if streams.Load() != 1 || sends.Load() != 1 || !strings.Contains(out.String(), id) {
		t.Fatalf("identity route lost content: streams=%d sends=%d data=%s", streams.Load(), sends.Load(), out.String())
	}
	if wrongRequests.Load() > 1 {
		t.Fatal("directory hint received session operations instead of just an identity probe")
	}
}

func TestSessionPortProbeRequiresExactNativeIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		found      bool
	}{
		{"matching", `{"id":"native"}`, http.StatusOK, true},
		{"another-session", `{"id":"other"}`, http.StatusOK, false},
		{"missing-id", `{}`, http.StatusOK, false},
		{"malformed", `{`, http.StatusOK, false},
		{"not-found", `{"id":"native"}`, http.StatusNotFound, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			port := fakePort(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/session/native" {
					t.Errorf("probe path: %s", r.URL.Path)
				}
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			})
			found, err := probeSessionPort(t.Context(), port, "native")
			if err != nil || found != tc.found {
				t.Fatalf("identity probe: found=%v err=%v", found, err)
			}
		})
	}
}

func TestSessionPortBindingDoesNotDependOnDirectoryAndHonorsCancellation(t *testing.T) {
	d := NewDiscovery()
	d.rememberSessionPort("native", "bound-port")
	port, err := d.resolveSessionPortCtx(t.Context(), "native", "/missing/renamed/project")
	if err != nil || port != "bound-port" {
		t.Fatalf("bound session consulted project metadata: %s %v", port, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := d.resolveSessionPortCtx(ctx, "native", "/missing"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled request reused session transport: %v", err)
	}
}

func TestActiveSessionObservationBindsRuntimeWithoutProjectLookup(t *testing.T) {
	for _, seed := range []bool{false, true} {
		t.Run(fmt.Sprintf("seed=%v", seed), func(t *testing.T) {
			a := New(newTestDBWithSession(t, "native", "/different-project"), nil, nil, ocapi.Auth{})
			a.rt = NewDiscovery()
			if seed {
				a.SeedSessionStatus("actual-port", 0, 0, map[string]string{"native": "busy"})
			} else {
				a.ObserveSessionStatus("actual-port", 0, "native", "busy")
			}
			if port := a.rt.preferredSessionPort("native"); port != "actual-port" {
				t.Fatalf("live native identity did not bind its runtime: %q", port)
			}
			a.ObserveSessionStatus("another-port", 0, "native", "idle")
			if port := a.rt.preferredSessionPort("native"); port != "actual-port" {
				t.Fatalf("unrelated settled observation stole runtime binding: %q", port)
			}
			if port := a.sessionRuntimePort("native"); port != "actual-port" {
				t.Fatalf("session info ignored the verified runtime binding: %q", port)
			}
			a.ClearSessionStatusForPort("actual-port")
			if port := a.rt.preferredSessionPort("native"); port != "" {
				t.Fatalf("retired runtime retained session bindings: %q", port)
			}
		})
	}
}
