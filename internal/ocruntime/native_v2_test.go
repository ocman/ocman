package ocruntime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/NoUseFreak/ocman/internal/ocapi"
	"github.com/NoUseFreak/ocman/internal/tmux"
)

func TestNativeLaunchV2(t *testing.T) {
	for _, tc := range []struct {
		name, db string
	}{{"no OPENCODE_DB", ""}, {"forwards OPENCODE_DB", "/tmp/oc-v2.db"}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("OPENCODE_DB", tc.db)
			f := &fakeLaunch{}
			rt := &NativeRuntime{launch: f.fn, auth: ocapi.New("pw")}
			if _, err := rt.Launch(t.Context(), LaunchSpec{
				RepoRoot: "/r", Port: 4242, PermissionJSON: `{"x":"allow"}`, V2: true,
			}); err != nil {
				t.Fatalf("Launch: %v", err)
			}
			if want := tmux.OpencodeServeCommandForPort(4242); f.command != want {
				t.Errorf("command = %q, want %q", f.command, want)
			}
			if _, ok := f.env["OPENCODE_PERMISSION"]; ok {
				t.Errorf("OPENCODE_PERMISSION set for v2: %v", f.env)
			}
			if f.env["OPENCODE_PASSWORD"] != "pw" {
				t.Errorf("OPENCODE_PASSWORD = %q, want pw", f.env["OPENCODE_PASSWORD"])
			}
			got, ok := f.env["OPENCODE_DB"]
			if tc.db == "" && ok {
				t.Errorf("OPENCODE_DB set to %q with empty process env", got)
			}
			if tc.db != "" && got != tc.db {
				t.Errorf("OPENCODE_DB = %q, want %q", got, tc.db)
			}
		})
	}
}

func TestProbeV2IdentityFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		match      bool
	}{
		{"matching non-repo location", "", http.StatusOK, true},
		{"unavailable", `{}`, http.StatusServiceUnavailable, false},
		{"malformed", `not-json`, http.StatusOK, false},
		{"missing directory", `{}`, http.StatusOK, false},
		{"empty directory", `{"directory":""}`, http.StatusOK, false},
		{"other root", `{"directory":"/other/database"}`, http.StatusOK, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/api/location" || r.URL.RawQuery != "" {
					t.Errorf("identity must read server default, got %s %s", r.Method, r.URL)
				}
				w.WriteHeader(tc.status)
				if tc.match {
					_ = json.NewEncoder(w).Encode(map[string]any{"directory": root, "project": map[string]any{"directory": "/"}})
				} else {
					_, _ = w.Write([]byte(tc.body))
				}
			}))
			defer server.Close()
			err := ProbeV2Identity(t.Context(), server.Client(), server.URL, root)
			if (err == nil) != tc.match || (!tc.match && !errors.Is(err, ErrProbeUnreachable)) {
				t.Fatalf("match=%v error=%v", tc.match, err)
			}
		})
	}
}

func TestProbeV2IdentityRequestErrors(t *testing.T) {
	if err := ProbeV2Identity(t.Context(), http.DefaultClient, "http://[", "/root"); !errors.Is(err, ErrProbeUnreachable) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := ProbeV2Identity(ctx, http.DefaultClient, "http://127.0.0.1:1", "/root"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) }))
	defer server.Close()
	client := &http.Client{Transport: ocapi.New("pw").Transport(nil)}
	if err := ProbeV2Identity(t.Context(), client, server.URL, "/root"); !errors.Is(err, ocapi.ErrAuthentication) {
		t.Fatal(err)
	}
}

func TestNativeLaunchV1IgnoresOpencodeDB(t *testing.T) {
	t.Setenv("OPENCODE_DB", "/tmp/oc.db")
	f := &fakeLaunch{}
	rt := &NativeRuntime{launch: f.fn}
	if _, err := rt.Launch(t.Context(), LaunchSpec{RepoRoot: "/r", Port: 4242, PermissionJSON: "{}"}); err != nil {
		t.Fatal(err)
	}
	if f.command != tmux.OpencodeCommandForPort(4242) {
		t.Errorf("command = %q", f.command)
	}
	if f.env["OPENCODE_PERMISSION"] != "{}" {
		t.Errorf("v1 lost OPENCODE_PERMISSION: %v", f.env)
	}
	if _, ok := f.env["OPENCODE_DB"]; ok {
		t.Errorf("v1 forwarded OPENCODE_DB: %v", f.env)
	}
}
