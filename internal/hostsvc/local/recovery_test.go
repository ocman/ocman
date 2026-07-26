package local

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/git"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/ocruntime"
)

// fakeStore is an in-memory ManagedStore for host recovery tests. It
// records Delete calls so a test can assert a dead row was discarded.
type fakeStore struct {
	mu      sync.Mutex
	rows    map[string]ManagedInstance
	deletes int
}

func newFakeStore() *fakeStore { return &fakeStore{rows: map[string]ManagedInstance{}} }

func (f *fakeStore) Upsert(repoRoot string, inst ManagedInstance) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows[repoRoot] = inst
	return nil
}

func (f *fakeStore) Get(repoRoot string) (ManagedInstance, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	mi, ok := f.rows[repoRoot]
	return mi, ok, nil
}

func (f *fakeStore) Delete(repoRoot string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.rows, repoRoot)
	f.deletes++
	return nil
}

func (f *fakeStore) has(repoRoot string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.rows[repoRoot]
	return ok
}

// TestEnsureProjectOpencode_RecoversFromStore covers AD-5: on a fresh Host
// (empty in-memory map, simulating a restart) a persisted store row is
// re-probed before it is trusted.
//   - Probe healthy  -> reuse, zero launches, row kept.
//   - Probe dead      -> row deleted + exactly one relaunch.
//
// The dead case is the red->green proof: without the store-recovery path,
// ensureLocked would never see the persisted row and would launch anyway;
// with a dead probe it must delete the stale row and relaunch exactly once.
func TestEnsureProjectOpencode_RecoversFromStore(t *testing.T) {
	tests := []struct {
		name         string
		probeHealthy bool
		wantLaunches int
		wantLaunched bool
		wantRowKept  bool
		wantDeletes  int
	}{
		{
			name:         "healthy persisted row is reused without launching",
			probeHealthy: true,
			wantLaunches: 0,
			wantLaunched: false,
			wantRowKept:  true,
			wantDeletes:  0,
		},
		{
			name:         "dead persisted row is discarded and relaunched",
			probeHealthy: false,
			wantLaunches: 1,
			wantLaunched: true,
			wantRowKept:  true, // relaunch re-upserts a fresh row
			wantDeletes:  1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repo := initRepo(t)
			ctx := context.Background()
			// The store is keyed by the canonical (resolved) repo root,
			// which is what ensureLocked looks up.
			repoRoot, err := git.ResolveRepoRoot(ctx, repo)
			if err != nil {
				t.Fatalf("resolve repo root: %v", err)
			}

			const persistedEndpoint = "http://127.0.0.1:41235"
			store := newFakeStore()
			store.rows[repoRoot] = ManagedInstance{
				Endpoint:   persistedEndpoint,
				Kind:       ocruntime.KindNativeTmux,
				RuntimeID:  "recovered-sess",
				LaunchedAt: time.Unix(1700000000, 0),
			}

			rt := &fakeRuntime{
				endpoint: "http://127.0.0.1:6666", // fresh launch endpoint
				probe: func(inst *ocruntime.Instance) bool {
					// The persisted instance probes per the case; a freshly
					// launched instance (different endpoint) is always up.
					if inst.Endpoint == persistedEndpoint {
						return tc.probeHealthy
					}
					return true
				},
			}

			// Fresh Host with an EMPTY in-memory map (New seeds it empty),
			// simulating an ocman restart: recovery must work from the
			// store alone.
			h := New(Deps{Runtime: rt, ManagedStore: store})
			h.portWaitTimeout = time.Second
			h.portWaitInterval = 5 * time.Millisecond

			res, err := h.EnsureProjectOpencode(ctx, hostsvc.EnsureProjectOpencodeRequest{ProjectDir: repo})
			if err != nil {
				t.Fatalf("EnsureProjectOpencode: %v", err)
			}
			if rt.launchCount() != tc.wantLaunches {
				t.Errorf("launches = %d; want %d", rt.launchCount(), tc.wantLaunches)
			}
			if res.Launched != tc.wantLaunched {
				t.Errorf("Launched = %v; want %v", res.Launched, tc.wantLaunched)
			}
			if store.deletes != tc.wantDeletes {
				t.Errorf("store deletes = %d; want %d", store.deletes, tc.wantDeletes)
			}
			if store.has(repoRoot) != tc.wantRowKept {
				t.Errorf("row present = %v; want %v", store.has(repoRoot), tc.wantRowKept)
			}
			if tc.probeHealthy && res.Endpoint != persistedEndpoint {
				t.Errorf("reused endpoint = %q; want persisted %q", res.Endpoint, persistedEndpoint)
			}
		})
	}
}

// TestEnsureProjectOpencode_PersistedRowCarriesRepoRoot pins that the
// recovered instance tells the runtime which project it must be serving.
// Loopback ports are recycled, so without this the probe cannot tell our
// dead instance's port from another project's live one.
func TestEnsureProjectOpencode_PersistedRowCarriesRepoRoot(t *testing.T) {
	repo := initRepo(t)
	ctx := context.Background()
	repoRoot, err := git.ResolveRepoRoot(ctx, repo)
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	const persistedEndpoint = "http://127.0.0.1:41235"
	store := newFakeStore()
	store.rows[repoRoot] = ManagedInstance{Endpoint: persistedEndpoint, Kind: ocruntime.KindNativeTmux, RuntimeID: "sess"}

	var probedRoot string
	rt := &fakeRuntime{
		endpoint: "http://127.0.0.1:6666",
		probe: func(inst *ocruntime.Instance) bool {
			if inst.Endpoint == persistedEndpoint {
				probedRoot = inst.RepoRoot
			}
			return true
		},
	}
	h := New(Deps{Runtime: rt, ManagedStore: store})
	h.portWaitTimeout = time.Second
	h.portWaitInterval = 5 * time.Millisecond

	if _, err := h.EnsureProjectOpencode(ctx, hostsvc.EnsureProjectOpencodeRequest{ProjectDir: repo}); err != nil {
		t.Fatalf("EnsureProjectOpencode: %v", err)
	}
	if probedRoot != repoRoot {
		t.Errorf("probed RepoRoot = %q; want %q", probedRoot, repoRoot)
	}
}
