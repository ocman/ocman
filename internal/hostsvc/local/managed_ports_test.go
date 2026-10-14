package local

import (
	"context"
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/ocruntime"
)

func TestManagedPortsRecoverOnlyHealthyOwnedInstances(t *testing.T) {
	store := newFakeStore()
	for _, root := range []string{"/healthy", "/stopped"} {
		store.rows[root] = ManagedInstance{Endpoint: "http://127.0.0.1:1001", RuntimeID: root}
	}
	rt := &fakeRuntime{probe: func(inst *ocruntime.Instance) bool { return inst.RepoRoot == "/healthy" }}
	h := New(Deps{Runtime: rt, ManagedStore: store})
	ports := h.ManagedOpenCodePorts(t.Context())
	if len(ports) != 1 || ports["/healthy"] != "1001" || rt.launchCount() != 0 {
		t.Fatalf("ports = %v; launches = %d", ports, rt.launchCount())
	}
	if h.authorizedEndpoint("/healthy") == "" || h.authorizedEndpoint("/stopped") != "" {
		t.Fatal("inventory bypassed identity authorization")
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
			h := New(Deps{Runtime: &probeErrorRuntime{err: tc.err}})
			inst := &ocruntime.Instance{Endpoint: "http://127.0.0.1:1001", RepoRoot: "/repo"}
			if tc.warm {
				h.authorizeInstance("/repo", inst)
			} else {
				h.setInstance("/repo", inst)
			}
			h.stopped["/repo"] = tc.stopped
			ports := h.ManagedOpenCodePorts(t.Context())
			if (ports["/repo"] == "1001") != tc.want {
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
	if ports := h.ManagedOpenCodePorts(t.Context()); ports == nil || len(ports) != 0 {
		t.Fatal(ports)
	}
	h = New(Deps{ManagedStore: failingListStore{}})
	if ports := h.ManagedOpenCodePorts(t.Context()); ports != nil {
		t.Fatal(ports)
	}
	h = New(Deps{})
	h.setInstance("/repo", &ocruntime.Instance{})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if ports := h.ManagedOpenCodePorts(ctx); ports != nil {
		t.Fatal(ports)
	}
}
