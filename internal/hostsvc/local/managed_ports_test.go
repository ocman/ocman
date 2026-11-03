package local

import (
	"context"
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/ocruntime"
)

func TestManagedPortsRecoverOnlyHealthyOwnedInstances(t *testing.T) {
	for _, healthy := range []bool{true, false} {
		t.Run(map[bool]string{true: "healthy", false: "stopped"}[healthy], func(t *testing.T) {
			root := machineRoot()
			store := newFakeStore()
			store.rows[root] = ManagedInstance{Endpoint: "http://127.0.0.1:1001", RuntimeID: "machine"}
			// Rows for any other root (a server for another database) are
			// never probed or published.
			store.rows["/other"] = ManagedInstance{Endpoint: "http://127.0.0.1:1002", RuntimeID: "other"}
			rt := &fakeRuntime{probe: func(inst *ocruntime.Instance) bool {
				if inst.RepoRoot != root {
					t.Errorf("probed non-machine root %q", inst.RepoRoot)
				}
				return healthy
			}}
			h := New(Deps{Runtime: rt, ManagedStore: store})
			ports := h.RefreshManagedOpenCodePorts(t.Context())
			if (ports[root] == "1001") != healthy || len(ports) > 1 || rt.launchCount() != 0 {
				t.Fatalf("ports = %v; launches = %d", ports, rt.launchCount())
			}
			if (h.authorizedEndpoint(root) != "") != healthy || h.authorizedEndpoint("/other") != "" {
				t.Fatal("inventory bypassed identity authorization")
			}
		})
	}
}

func TestManagedPortsRespectRoutingAndStopState(t *testing.T) {
	for _, tc := range []struct {
		name                string
		err                 error
		warm, stopped, want bool
	}{
		{"healthy memory", nil, true, false, true},
		{"warm timeout", context.DeadlineExceeded, true, false, true},
		{"cold timeout", context.DeadlineExceeded, false, false, false},
		{"rejected", ocruntime.ErrProbeIdentityMismatch, true, false, false},
		{"stopped", nil, true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := machineRoot()
			h := New(Deps{Runtime: &probeErrorRuntime{err: tc.err}})
			inst := &ocruntime.Instance{Endpoint: "http://127.0.0.1:1001", RepoRoot: root}
			if tc.warm {
				h.authorizeInstance(root, inst)
			} else {
				h.setInstance(root, inst)
			}
			h.stopped[root] = tc.stopped
			ports := h.RefreshManagedOpenCodePorts(t.Context())
			if (ports[root] == "1001") != tc.want {
				t.Fatalf("ports = %v", ports)
			}
		})
	}
}

type failingListStore struct{ ManagedStore }

func (failingListStore) List(context.Context) (map[string]ManagedInstance, error) {
	return nil, errors.New("inventory unavailable")
}

func TestManagedPortsDisabledCanceledAndStoreFailure(t *testing.T) {
	h := New(Deps{PlatformEnabled: func(string) bool { return false }})
	if ports := h.RefreshManagedOpenCodePorts(t.Context()); ports == nil || len(ports) != 0 {
		t.Fatal(ports)
	}
	h = New(Deps{ManagedStore: failingListStore{}})
	if ports := h.RefreshManagedOpenCodePorts(t.Context()); ports != nil {
		t.Fatal(ports)
	}
	h = New(Deps{})
	h.setInstance(machineRoot(), &ocruntime.Instance{})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if ports := h.RefreshManagedOpenCodePorts(ctx); ports != nil {
		t.Fatal(ports)
	}
}
