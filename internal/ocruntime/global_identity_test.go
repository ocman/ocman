package ocruntime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms/opencode/api"
)

func TestNativeProbe_GlobalProjectDirectory(t *testing.T) {
	const directory = "/home/u/non-git-project"
	for _, tc := range []struct {
		name   string
		body   string
		status int
		want   error
	}{
		{name: "matching directory", body: `{"directory":"` + directory + `","worktree":"/"}`},
		{name: "foreign directory", body: `{"directory":"/home/u/other"}`, want: ErrProbeIdentityMismatch},
		{name: "missing directory", body: `{}`, want: ErrProbeNotReady},
		{name: "empty directory", body: `{"directory":""}`, want: ErrProbeNotReady},
		{name: "malformed payload", body: `{`, want: ErrProbeNotReady},
		{name: "unsupported endpoint", status: http.StatusNotFound, want: ErrProbeNotReady},
		{name: "temporarily unavailable", status: http.StatusServiceUnavailable, want: ErrProbeNotReady},
		{name: "authentication rejected", status: http.StatusUnauthorized, want: ocapi.ErrAuthentication},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.RawQuery != "" {
					t.Errorf("identity probe must not override the server directory: %s", r.URL)
				}
				switch r.URL.Path {
				case "/config":
					w.WriteHeader(http.StatusOK)
				case "/project/current":
					_ = json.NewEncoder(w).Encode(map[string]string{"id": "global", "worktree": "/"})
				case "/path":
					if tc.status != 0 {
						w.WriteHeader(tc.status)
					}
					_, _ = w.Write([]byte(tc.body))
				default:
					t.Errorf("unexpected probe: %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer srv.Close()
			err := NewNativeRuntime().Probe(t.Context(), &Instance{Endpoint: srv.URL, RepoRoot: directory})
			if !errors.Is(err, tc.want) {
				t.Fatalf("Probe = %v, want %v", err, tc.want)
			}
			if errors.Is(tc.want, ErrProbeNotReady) && errors.Is(err, ErrProbeIdentityMismatch) {
				t.Fatal("unavailable directory must not positively reject the server")
			}
		})
	}
}

func TestGlobalDirectoryTransportErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, tc := range []struct {
		name     string
		endpoint string
		ctx      context.Context
	}{
		{name: "invalid endpoint", endpoint: "://invalid", ctx: t.Context()},
		{name: "cancelled request", endpoint: "http://127.0.0.1:1", ctx: ctx},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := probeGlobalDirectory(tc.ctx, http.DefaultClient, tc.endpoint, "/home/u/project")
			if !errors.Is(err, ErrProbeUnreachable) || errors.Is(err, ErrProbeIdentityMismatch) {
				t.Fatalf("transport failure must remain inconclusive: %v", err)
			}
			if tc.ctx.Err() != nil && !errors.Is(err, tc.ctx.Err()) {
				t.Fatalf("lost cancellation error: %v", err)
			}
		})
	}
}
