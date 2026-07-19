package ocruntime

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
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

func TestNativeLaunch_ThreadsPortAndPermission(t *testing.T) {
	f := &fakeLaunch{}
	rt := &NativeRuntime{launch: f.fn}

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

	inst, err := rt.Launch(context.Background(), LaunchSpec{RepoRoot: "/r", Port: 9},
	)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if inst.Endpoint != "http://127.0.0.1:9" {
		t.Errorf("Endpoint = %q, want default host", inst.Endpoint)
	}
	if _, ok := f.env["OPENCODE_PERMISSION"]; ok {
		t.Error("OPENCODE_PERMISSION should be absent when PermissionJSON empty")
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
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/config" {
			t.Errorf("probed path = %q, want /config", r.URL.Path)
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

	rt := NewNativeRuntime()
	tests := []struct {
		name string
		inst *Instance
		want bool
	}{
		{"200", &Instance{Endpoint: ok.URL}, true},
		{"500", &Instance{Endpoint: bad.URL}, false},
		{"refused", &Instance{Endpoint: refused}, false},
		{"nil instance", nil, false},
		{"empty endpoint", &Instance{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := rt.Probe(context.Background(), tt.inst); got != tt.want {
				t.Errorf("Probe = %v, want %v", got, tt.want)
			}
		})
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
