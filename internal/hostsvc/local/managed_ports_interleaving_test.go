package local

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/ocruntime"
	"github.com/NoUseFreak/ocman/internal/platforms/opencode"
)

type inventoryRuntime struct {
	*fakeRuntime
	probeFn func(context.Context, *ocruntime.Instance) error
}

func (r inventoryRuntime) Probe(ctx context.Context, inst *ocruntime.Instance) error {
	return r.probeFn(ctx, inst)
}

func TestInventoryCannotJoinOrSuppressRuntimeMutations(t *testing.T) {
	for _, operation := range []string{"restart", "stop", "ensure"} {
		t.Run(operation, func(t *testing.T) {
			root, err := projectOpencodeRoot(t.Context(), initRepo(t))
			if err != nil {
				t.Fatal(err)
			}
			started, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			t.Cleanup(unblock)
			var calls atomic.Int32
			rt := inventoryRuntime{fakeRuntime: &fakeRuntime{endpoint: "http://127.0.0.1:1002"}}
			rt.probeFn = func(ctx context.Context, inst *ocruntime.Instance) error {
				if calls.Add(1) == 1 {
					close(started)
					select {
					case <-release:
					case <-ctx.Done():
						return ctx.Err()
					}
				}
				if operation == "ensure" && inst.Endpoint == "http://127.0.0.1:1001" {
					return ocruntime.ErrProbeUnreachable
				}
				return nil
			}
			h := New(Deps{Runtime: rt})
			h.authorizeInstance(root, &ocruntime.Instance{Endpoint: "http://127.0.0.1:1001", RepoRoot: root, Kind: ocruntime.KindNativeTmux, ID: "old"})
			inventory := make(chan map[string]string, 1)
			go func() { inventory <- h.RefreshManagedOpenCodePorts(t.Context()) }()
			<-started
			mutation := make(chan error, 1)
			go func() {
				req := hostsvc.EnsureProjectOpencodeRequest{ProjectDir: root}
				var err error
				if operation == "stop" {
					err = h.StopProjectOpencode(t.Context(), req)
				} else {
					var result *hostsvc.EnsureRuntimeResult
					if operation == "ensure" {
						result, err = h.EnsureProjectOpencode(t.Context(), req)
					} else {
						result, err = h.RestartProjectOpencode(t.Context(), req)
					}
					if err == nil && !result.Launched {
						err = errors.New("inventory satisfied a requested mutation")
					}
				}
				mutation <- err
			}()
			select {
			case err := <-mutation:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("inventory blocked a runtime mutation")
			}
			if rt.stopCount() != 1 {
				t.Fatalf("stop was skipped: %d", rt.stopCount())
			}
			unblock()
			ports := <-inventory
			if ports[root] == "1001" {
				t.Fatal("stale probe reauthorized the replaced/stopped instance")
			}
			if operation != "stop" && h.authorizedEndpoint(root) != "http://127.0.0.1:1002" {
				t.Fatal("stale probe revoked replacement authorization")
			}
		})
	}
}

func TestInventoryCannotRestoreClearedOrRevokedAuthorization(t *testing.T) {
	for _, change := range []string{"clear warm", "revoke warm", "clear cold"} {
		t.Run(change, func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			rt := inventoryRuntime{fakeRuntime: &fakeRuntime{}, probeFn: func(ctx context.Context, _ *ocruntime.Instance) error {
				close(started)
				select {
				case <-release:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}}
			store := newFakeStore()
			store.rows["/repo"] = ManagedInstance{Endpoint: "http://127.0.0.1:1001", RuntimeID: "old"}
			h := New(Deps{Runtime: rt, ManagedStore: store})
			if change != "clear cold" {
				h.authorizeInstance("/repo", &ocruntime.Instance{Endpoint: "http://127.0.0.1:1001", ID: "old"})
			}
			result := make(chan map[string]string, 1)
			go func() { result <- h.RefreshManagedOpenCodePorts(t.Context()) }()
			<-started
			if change == "revoke warm" {
				h.revokeInstanceAuthorization("/repo")
			} else {
				h.clearInstance("/repo")
			}
			close(release)
			if ports := <-result; len(ports) != 0 || h.authorizedEndpoint("/repo") != "" {
				t.Fatalf("stale probe restored authorization: %v", ports)
			}
		})
	}
}

func TestExpiredDiscoveryInsideReplacementDoesNotReenterMutationFlight(t *testing.T) {
	root, err := projectOpencodeRoot(t.Context(), initRepo(t))
	if err != nil {
		t.Fatal(err)
	}
	rt := inventoryRuntime{fakeRuntime: &fakeRuntime{endpoint: "http://127.0.0.1:1002"}, probeFn: func(_ context.Context, inst *ocruntime.Instance) error {
		if inst.Endpoint == "http://127.0.0.1:1001" {
			return ocruntime.ErrProbeUnreachable
		}
		return nil
	}}
	h := New(Deps{Runtime: rt})
	h.authorizeInstance(root, &ocruntime.Instance{Endpoint: "http://127.0.0.1:1001", RepoRoot: root, ID: "old"})
	opencode.SetManagedPortSource(h.ManagedOpenCodePorts)
	t.Cleanup(func() {
		opencode.SetManagedPortSource(func(context.Context) map[string]string { return map[string]string{} })
	})
	h.deps.BeforeReplace = func(ctx context.Context, _, _ string) error {
		readCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
		defer cancel()
		opencode.InvalidateOpenCodePortCache()
		_ = opencode.DiscoverOpenCodePortsContext(readCtx)
		return readCtx.Err()
	}
	if _, err := h.RestartProjectOpencode(t.Context(), hostsvc.EnsureProjectOpencodeRequest{ProjectDir: root}); err != nil {
		t.Fatalf("lifecycle/discovery callback blocked replacement: %v", err)
	}
	if rt.launchCount() != 1 {
		t.Fatal("replacement did not launch")
	}
}

func TestInventoryHonorsDurableStopFencesWithoutRunningMutations(t *testing.T) {
	for _, scenario := range []string{"stopped", "cold pending", "warm pending", "different pending", "stopped query error", "pending query error"} {
		t.Run(scenario, func(t *testing.T) {
			store := newFakeStore()
			inst := &ocruntime.Instance{Endpoint: "http://127.0.0.1:1001", RepoRoot: "/repo", ID: "owned"}
			store.rows["/repo"] = ManagedInstance{Endpoint: inst.Endpoint, RuntimeID: inst.ID}
			rt := &probeErrorRuntime{err: context.DeadlineExceeded}
			h := New(Deps{Runtime: rt, ManagedStore: store,
				BeforeReplace: func(context.Context, string, string) error { t.Fatal("inventory ran replacement"); return nil },
				ReplacementStopped: func(context.Context, string) (bool, error) {
					if scenario == "stopped query error" {
						return false, errors.New("unavailable")
					}
					return scenario == "stopped", nil
				},
				ReplacementStopping: func(context.Context, string) (*ocruntime.Instance, error) {
					if scenario == "pending query error" {
						return nil, errors.New("unavailable")
					}
					pending := *inst
					if scenario == "different pending" {
						pending.ID = "other"
					}
					return &pending, nil
				},
			})
			if scenario == "warm pending" || scenario == "different pending" {
				h.authorizeInstance("/repo", inst)
			}
			ports := h.RefreshManagedOpenCodePorts(t.Context())
			if (ports["/repo"] == "1001") != (scenario == "warm pending") {
				t.Fatal(ports)
			}
			if (ports == nil) != (scenario == "stopped query error" || scenario == "pending query error") {
				t.Fatal("query failure became successful emptiness")
			}
		})
	}
}
