package ocruntime

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/ocapi"
)

// TestAllocateLoopbackPort_BindsSuccessfully is the runnable self-check
// the ticket requires: an allocated port must then bind.
func TestAllocateLoopbackPort_BindsSuccessfully(t *testing.T) {
	port, err := AllocateLoopbackPort()
	if err != nil {
		t.Fatalf("AllocateLoopbackPort: %v", err)
	}
	if port <= 0 || port > 65535 {
		t.Fatalf("port %d out of range", port)
	}
	l, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatalf("allocated port %d did not bind: %v", port, err)
	}
	l.Close()
}

// fakeLaunch records the launch call so we can assert the port was
// threaded into the pane command.
type fakeLaunch struct {
	dir, command string
	env          map[string]string
	session      string
	err          error
	calls        int
}

func (f *fakeLaunch) fn(dir, command string, env map[string]string) (string, error) {
	f.calls++
	f.dir, f.command, f.env = dir, command, env
	if f.session == "" {
		f.session = "~/src/repo"
	}
	return f.session, f.err
}

func TestNativeLaunch_ThreadsPortPermissionAndPassword(t *testing.T) {
	f := &fakeLaunch{}
	rt := &NativeRuntime{launch: f.fn, auth: ocapi.New("managed-secret")}

	inst, err := rt.Launch(context.Background(), LaunchSpec{
		RepoRoot:       "/home/u/src/repo",
		Port:           41235,
		PermissionJSON: `{"external_directory":{}}`,
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}

	if f.calls != 1 {
		t.Fatalf("launch calls = %d, want 1", f.calls)
	}
	// Port threaded into the command, not --port 0.
	if !strings.Contains(f.command, "--port 41235") {
		t.Errorf("command %q missing --port 41235", f.command)
	}
	if strings.Contains(f.command, "--port 0") {
		t.Errorf("command %q still uses --port 0", f.command)
	}
	// OPENCODE_PERMISSION seeded.
	if got := f.env["OPENCODE_PERMISSION"]; got != `{"external_directory":{}}` {
		t.Errorf("OPENCODE_PERMISSION = %q, want seeded JSON", got)
	}
	if got := f.env["OPENCODE_SERVER_PASSWORD"]; got != "managed-secret" {
		t.Error("OPENCODE_SERVER_PASSWORD was not injected")
	}
	if got := f.env["OPENCODE_SERVER_USERNAME"]; got != ocapi.DefaultUsername {
		t.Errorf("OPENCODE_SERVER_USERNAME = %q", got)
	}
	if strings.Contains(f.command, "managed-secret") {
		t.Error("password exposed in pane command")
	}
	encoded, err := json.Marshal(inst)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "managed-secret") {
		t.Error("password exposed in runtime diagnostics")
	}
	// Instance shape.
	if inst.Endpoint != "http://127.0.0.1:41235" {
		t.Errorf("Endpoint = %q, want http://127.0.0.1:41235", inst.Endpoint)
	}
	if inst.Kind != KindNativeTmux {
		t.Errorf("Kind = %q, want %q", inst.Kind, KindNativeTmux)
	}
	if inst.ID != "~/src/repo" {
		t.Errorf("ID = %q, want the tmux session name", inst.ID)
	}
}

func TestNativeLaunch_DefaultsHostAndOmitsEmptyPermission(t *testing.T) {
	f := &fakeLaunch{session: "sess"}
	rt := &NativeRuntime{launch: f.fn}

	inst, err := rt.Launch(context.Background(), LaunchSpec{RepoRoot: "/r", Port: 9})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if inst.Endpoint != "http://127.0.0.1:9" {
		t.Errorf("Endpoint = %q, want default host", inst.Endpoint)
	}
	if _, ok := f.env["OPENCODE_PERMISSION"]; ok {
		t.Error("OPENCODE_PERMISSION should be absent when PermissionJSON empty")
	}
	if _, ok := f.env["OPENCODE_SERVER_PASSWORD"]; ok {
		t.Error("OPENCODE_SERVER_PASSWORD should be absent when auth disabled")
	}
	if _, ok := f.env["OPENCODE_SERVER_USERNAME"]; ok {
		t.Error("OPENCODE_SERVER_USERNAME should be absent when auth disabled")
	}
}

func TestNativeLaunch_Validation(t *testing.T) {
	rt := &NativeRuntime{launch: (&fakeLaunch{}).fn}
	tests := []struct {
		name string
		spec LaunchSpec
	}{
		{"no repo root", LaunchSpec{Port: 1}},
		{"zero port", LaunchSpec{RepoRoot: "/r", Port: 0}},
		{"negative port", LaunchSpec{RepoRoot: "/r", Port: -1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := rt.Launch(context.Background(), tt.spec); err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

func TestNativeProbe(t *testing.T) {
	const password = "probe-secret"
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/config" {
			t.Errorf("probed path = %q, want /config", r.URL.Path)
		}
		_, pass, valid := r.BasicAuth()
		if !valid || pass != password {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ok.Close()

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer bad.Close()

	// A refused endpoint: allocate a port then don't listen on it.
	refusedPort, err := AllocateLoopbackPort()
	if err != nil {
		t.Fatalf("alloc: %v", err)
	}
	refused := "http://127.0.0.1:" + strconv.Itoa(refusedPort)

	rt := NewNativeRuntimeWithAuth(ocapi.New(password))
	tests := []struct {
		name string
		inst *Instance
		want error
	}{
		{"200", &Instance{Endpoint: ok.URL}, nil},
		{"500", &Instance{Endpoint: bad.URL}, ErrProbeUnreachable},
		{"refused", &Instance{Endpoint: refused}, ErrProbeUnreachable},
		{"nil instance", nil, ErrProbeUnreachable},
		{"empty endpoint", &Instance{}, ErrProbeUnreachable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := rt.Probe(context.Background(), tt.inst); !errors.Is(got, tt.want) {
				t.Errorf("Probe = %v, want %v", got, tt.want)
			}
		})
	}

	wrong := NewNativeRuntimeWithAuth(ocapi.New("wrong"))
	if err := wrong.Probe(context.Background(), &Instance{Endpoint: ok.URL}); !errors.Is(err, ocapi.ErrAuthentication) {
		t.Fatalf("wrong credential probe = %v, want authentication error", err)
	}
}

func TestNativeStop(t *testing.T) {
	var killed string
	rt := &NativeRuntime{kill: func(s string) error { killed = s; return nil }}

	if err := rt.Stop(context.Background(), &Instance{ID: "~/src/repo"}); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if killed != "~/src/repo" {
		t.Errorf("killed session = %q, want ~/src/repo", killed)
	}

	// No ID -> error, no kill.
	killed = ""
	if err := rt.Stop(context.Background(), &Instance{}); err == nil {
		t.Error("expected error stopping instance without ID")
	}
	if err := rt.Stop(context.Background(), nil); err == nil {
		t.Error("expected error stopping nil instance")
	}
}
