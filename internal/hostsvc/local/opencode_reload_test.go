package local

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/ocapi"
	"github.com/NoUseFreak/ocman/internal/ocruntime"
	"github.com/NoUseFreak/ocman/internal/ocv2"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

func TestReloadOpencodeRefreshesExistingMachineServer(t *testing.T) {
	for _, persisted := range []bool{false, true} {
		t.Run(map[bool]string{false: "memory", true: "persisted"}[persisted], func(t *testing.T) {
			h, rt, store, _, root := v2Host(t)
			calls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				user, password, ok := r.BasicAuth()
				if r.Method != http.MethodPost || r.URL.Path != "/api/location/reload" || !ok || user != "opencode" || password != "secret" {
					t.Errorf("request = %s %s, auth=%v", r.Method, r.URL, ok)
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			defer upstream.Close()
			h.deps.OpenCodeAuth = func() ocapi.Auth { return ocapi.New("secret") }
			reloaded := ""
			h.deps.OpenCodeReloaded = func(port string) { reloaded = port }
			if persisted {
				if err := store.Upsert(t.Context(), root, ManagedInstance{Endpoint: upstream.URL}); err != nil {
					t.Fatal(err)
				}
			} else {
				h.setInstance(root, &ocruntime.Instance{Endpoint: upstream.URL})
			}
			// An unrelated project's server must never receive the machine reload.
			h.setInstance("/other/project", &ocruntime.Instance{Endpoint: "http://127.0.0.1:1"})
			if err := h.ReloadOpencode(t.Context()); err != nil {
				t.Fatal(err)
			}
			if calls != 1 || rt.launchCount() != 0 || rt.stopCount() != 0 {
				t.Fatalf("calls=%d launches=%d stops=%d", calls, rt.launchCount(), rt.stopCount())
			}
			if reloaded == "" {
				t.Fatal("successful reload did not invalidate catalogs")
			}
		})
	}
}

func TestReloadOpencodeErrorsDoNotRestart(t *testing.T) {
	for _, code := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusServiceUnavailable, http.StatusOK} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			h, rt, _, _, root := v2Host(t)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) }))
			defer upstream.Close()
			h.setInstance(root, &ocruntime.Instance{Endpoint: upstream.URL})
			err := h.ReloadOpencode(t.Context())
			if err == nil {
				t.Fatal("want upstream error")
			}
			if (code == 401 || code == 403) && !errors.Is(err, ocapi.ErrAuthentication) {
				t.Fatalf("error = %v, want authentication error", err)
			}
			if rt.launchCount() != 0 || rt.stopCount() != 0 {
				t.Fatal("reload failure restarted the server")
			}
		})
	}
}

func TestReloadOpencodeMissingUnsupportedAndCancelled(t *testing.T) {
	h, rt, _, _, root := v2Host(t)
	if err := h.ReloadOpencode(t.Context()); err == nil || !strings.Contains(err.Error(), "no managed") {
		t.Fatalf("missing server error = %v", err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer upstream.Close()
	h.setInstance(root, &ocruntime.Instance{Endpoint: upstream.URL})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := h.ReloadOpencode(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled error = %v", err)
	}
	undo := ocv2.SetInstalledV2(false)
	defer undo()
	if err := h.ReloadOpencode(t.Context()); !errors.Is(err, platforms.ErrUnsupported) {
		t.Fatalf("v1 error = %v", err)
	}
	if rt.launchCount() != 0 || rt.stopCount() != 0 {
		t.Fatal("reload changed server lifecycle")
	}
}
