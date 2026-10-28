package ocruntime

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/providerprotocol"
	"github.com/ocman/ocman/internal/providersupervisor/providertest"
	"github.com/ocman/ocman/internal/testutil"
)

func TestProviderLaunchAndStop(t *testing.T) {
	dir := providertest.Directory(t)
	r := NewBackendRuntime("fake", "/fake executable", dir)
	addr := "127.0.0.1:1"
	r.Args = func() []string { return []string{"--mcp-addr", addr} }
	addr = "127.0.0.1:2" // read at launch, not when configured
	var launchArgs []string
	r.launch = func(_ context.Context, root string, args []string) error {
		launchArgs = args
		if root != "/repo space" {
			t.Fatal(root)
		}
		return nil
	}
	inst, err := r.Launch(t.Context(), LaunchSpec{RepoRoot: "/repo space"})
	if err != nil {
		t.Fatal(err)
	}
	if inst.Kind != KindProviderTmux || !strings.HasPrefix(inst.Endpoint, "unix://"+dir) || !strings.Contains(strings.Join(launchArgs, "|"), "--|/fake executable|provider|fake|--socket|") || !strings.HasSuffix(strings.Join(launchArgs, "|"), "|--mcp-addr|127.0.0.1:2") {
		t.Fatal(inst, launchArgs)
	}
	info, err := os.Stat(filepath.Dir(strings.TrimPrefix(inst.Endpoint, "unix://")))
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatal(info, err)
	}
	r.stop = func(_ context.Context, id string) error {
		if id != inst.ID {
			t.Fatal(id)
		}
		return nil
	}
	if err := r.Stop(t.Context(), inst); err != nil {
		t.Fatal(err)
	}
	if err := r.Stop(t.Context(), nil); err == nil {
		t.Fatal("nil stop")
	}
	r.alive = func(context.Context, string) (bool, error) { return true, nil }
	if alive, err := r.Alive(t.Context(), inst); err != nil || !alive {
		t.Fatal(alive, err)
	}
	if _, err := r.Alive(t.Context(), nil); err == nil {
		t.Fatal("invalid live handle")
	}
	if _, err := r.Launch(t.Context(), LaunchSpec{}); err == nil {
		t.Fatal("empty root")
	}
	r.Directory = "/" + strings.Repeat("a", 110)
	if _, err := r.Launch(t.Context(), LaunchSpec{RepoRoot: "/repo"}); err == nil {
		t.Fatal("long socket")
	}
	r.Directory = dir
	r.launch = func(context.Context, string, []string) error { return errors.New("launch failed") }
	if _, err := r.Launch(t.Context(), LaunchSpec{RepoRoot: "/repo"}); err == nil {
		t.Fatal("launch failure lost")
	}
}

func TestProviderProbeVersionsAndMalformedResponses(t *testing.T) {
	dir := providertest.Directory(t)
	socket := filepath.Join(dir, "s")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	var response any = providerprotocol.Hello{PlatformID: "fake", CLIVersion: "test", ProtocolVersion: providerprotocol.CurrentVersion}
	status := 200
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(response)
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	r := NewBackendRuntime("fake", "/binary", dir)
	inst := &Instance{Endpoint: "unix://" + socket}
	for _, version := range []int{providerprotocol.CurrentVersion, providerprotocol.PreviousVersion, 99} {
		response = providerprotocol.Hello{PlatformID: "fake", CLIVersion: "test", ProtocolVersion: version}
		err := r.Probe(t.Context(), inst)
		if version == 99 {
			var skew *providerprotocol.VersionError
			if !errors.As(err, &skew) {
				t.Fatal(err)
			}
		} else if err != nil {
			t.Fatal(err)
		}
	}
	for _, bad := range []any{map[string]any{"providerId": "fake"}, providerprotocol.Hello{PlatformID: "other", CLIVersion: "test"}, "bad"} {
		response = bad
		if err := r.Probe(t.Context(), inst); !errors.Is(err, ErrProbeUnreachable) {
			t.Fatal(err)
		}
	}
	response = map[string]any{"idle": false}
	if idle, err := r.Drain(t.Context(), inst); err != nil || idle {
		t.Fatal(idle, err)
	}
	response = map[string]any{"idle": true}
	if idle, err := r.Drain(t.Context(), inst); err != nil || !idle {
		t.Fatal(idle, err)
	}
	response = map[string]any{}
	if _, err := r.Drain(t.Context(), inst); err == nil {
		t.Fatal("missing idle")
	}
	status = 500
	if err := r.Probe(t.Context(), inst); !errors.Is(err, ErrProbeUnreachable) {
		t.Fatal(err)
	}
	for _, bad := range []*Instance{nil, {Endpoint: "http://localhost"}, {Endpoint: "unix://host/path"}, {Endpoint: "%"}} {
		if err := r.Probe(t.Context(), bad); !errors.Is(err, ErrProbeUnreachable) {
			t.Fatal(err)
		}
	}
}

// The launch configuration is part of the instance id: a supervisor
// launched with another one is not current.
func TestProviderIdentity(t *testing.T) {
	dir := testutil.ShortTempDir(t, "id-") // short: unix socket path limit
	r := NewBackendRuntime("fake", "/fake", dir)
	r.launch = func(context.Context, string, []string) error { return nil }
	if !r.Current(&Instance{ID: "fake-old-name"}) {
		t.Fatal("without an identity every instance is current")
	}
	config := "a"
	r.Identity = func() string { return config }
	inst, err := r.Launch(t.Context(), LaunchSpec{RepoRoot: "/repo"})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Current(inst) || r.Current(nil) {
		t.Fatal("identity of its own launch")
	}
	config = "b"
	if r.Current(inst) {
		t.Fatal("a changed configuration must not reuse the instance")
	}
}
