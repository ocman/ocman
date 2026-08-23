package remote

import (
	"context"
	"database/sql"
	"sync/atomic"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

// newStateDB opens an in-memory state.db for Manager tests.
func newStateDB(t *testing.T) *state.DB {
	t.Helper()
	raw, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { raw.Close() })
	d, err := state.OpenFromSQL(raw)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// waitForRegister polls until the compound platform id appears in reg.
func waitForRegister(t *testing.T, reg *platforms.Registry, id string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := reg.Get(platforms.ID(id)); ok {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("platform %q never registered", id)
}

func TestManager_AddUpdateReconnectRemoveLifecycle(t *testing.T) {
	// Remote side.
	remoteReg := platforms.NewRegistry()
	remoteReg.Register(&fakePlatform{id: "opencode", sessions: []db.Session{{ID: "rs1", Platform: "opencode"}}})
	addr := startRealRemote(t, "tok", "abc123", remoteReg)

	store := newStateDB(t)
	hubReg := platforms.NewRegistry()
	router := hostsvc.NewRouter(localStubHost{})
	mgr := NewManager(hubReg, router, store, "opencode")
	t.Cleanup(mgr.Stop)
	ctx := context.Background()

	// Add dials in the background and registers the adapter on connect.
	id, err := mgr.Add(t.Context(), addr, "tok", "Workstation")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	waitForRegister(t, hubReg, "r-abc123:opencode")

	// Prime the ownership cache via a Sessions() fan-out (as the real
	// /api/sessions path does) so List can report a session count.
	if p, ok := hubReg.Get(platforms.ID("r-abc123:opencode")); ok {
		if _, err := p.Sessions(ctx, "", 0); err != nil {
			t.Fatalf("priming Sessions: %v", err)
		}
	}

	// List merges persisted config + live health + session count.
	list, err := mgr.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Health != string(HealthConnected) || list[0].SessionCount != 1 {
		t.Fatalf("unexpected list: %+v", list)
	}

	// Conn returns the live connection.
	if _, ok := mgr.Conn(id); !ok {
		t.Fatal("Conn should return the managed connection")
	}

	// Update reconnects with the new config.
	if err := mgr.Update(t.Context(), id, "Renamed", addr, true, nil); err != nil {
		t.Fatalf("Update: %v", err)
	}
	waitForRegister(t, hubReg, "r-abc123:opencode")

	// Reconnect is idempotent while enabled.
	if err := mgr.Reconnect(t.Context(), id); err != nil {
		t.Fatalf("Reconnect: %v", err)
	}
	waitForRegister(t, hubReg, "r-abc123:opencode")

	// Remove tears down the adapter and deletes the row.
	if err := mgr.Remove(t.Context(), id); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, ok := hubReg.Get(platforms.ID("r-abc123:opencode")); ok {
		t.Error("platform should be unregistered after Remove")
	}
	rest, _ := mgr.List(t.Context())
	if len(rest) != 0 {
		t.Fatalf("expected empty after remove, got %+v", rest)
	}
}

func TestManager_DisabledRemoteNotDialed(t *testing.T) {
	store := newStateDB(t)
	id, err := store.AddRemote(t.Context(), "127.0.0.1:59998", "tok", "Off")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateRemoteConfig(t.Context(), id, "Off", "127.0.0.1:59998", false, nil); err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(platforms.NewRegistry(), hostsvc.NewRouter(localStubHost{}), store, "opencode")
	t.Cleanup(mgr.Stop)
	mgr.Start(context.Background())

	if _, ok := mgr.Conn(id); ok {
		t.Error("disabled remote must not be dialed")
	}
}

func TestManager_EnabledRemotesAndInventoryLoop(t *testing.T) {
	remoteReg := platforms.NewRegistry()
	remoteReg.Register(&fakePlatform{id: "opencode"})
	addr := startRealRemote(t, "tok", "abc123", remoteReg)

	store := newStateDB(t)
	mgr := NewManager(platforms.NewRegistry(), hostsvc.NewRouter(localStubHost{}), store, "opencode")
	t.Cleanup(mgr.Stop)
	ctx := context.Background()
	if _, err := mgr.Add(t.Context(), addr, "tok", "Box"); err != nil {
		t.Fatal(err)
	}
	waitForRegister(t, mgr.registry, "r-abc123:opencode")

	enabled := mgr.EnabledRemotes()
	if len(enabled) != 1 || enabled[0].RemoteID != "abc123" || enabled[0].Platform != "r-abc123:opencode" {
		t.Fatalf("EnabledRemotes = %+v", enabled)
	}
	if enabled[0].RemoteName != "Box" {
		t.Errorf("RemoteName = %q, want Box", enabled[0].RemoteName)
	}
	deadline := time.Now().Add(time.Second)
	for mgr.resolveDir("/home/u/app") != "abc123" && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := mgr.resolveDir("/home/u/app"); got != "abc123" {
		t.Fatalf("connect-time inventory was not refreshed: owner = %q", got)
	}

	// RunInventoryLoop ticks at least once then exits on ctx cancel.
	loopCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { mgr.RunInventoryLoop(loopCtx, 10*time.Millisecond); close(done) }()
	time.Sleep(40 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("RunInventoryLoop did not exit on cancel")
	}
}

func TestManager_InventoryLoopKeepsRoutingInventoryFreshWithoutProjectsDemand(t *testing.T) {
	mgr := newInvManager(t)
	var calls atomic.Int32
	mgr.refreshInventories = func(context.Context) { calls.Add(1) }
	ctx, cancel := context.WithCancel(context.Background())
	go mgr.RunInventoryLoop(ctx, time.Millisecond)
	time.Sleep(10 * time.Millisecond)
	cancel()
	if got := calls.Load(); got == 0 {
		t.Fatal("routing inventory stopped without projects demand")
	}

	before := calls.Load()
	mgr.RefreshInventories(context.Background())
	if got := calls.Load(); got != before {
		t.Fatalf("explicit refresh used periodic test callback: got %d, want %d", got, before)
	}
}

// TestManager_RemoveEvictsInventory pins that removing a remote also
// drops its cached project inventory. Otherwise resolveDir keeps
// returning the dead remote ID, ForDir degrades that to local, and
// dir-scoped actions silently run on the hub instead.
func TestManager_RemoveEvictsInventory(t *testing.T) {
	remoteReg := platforms.NewRegistry()
	remoteReg.Register(&fakePlatform{id: "opencode"})
	addr := startRealRemote(t, "tok", "abc123", remoteReg)

	store := newStateDB(t)
	mgr := NewManager(platforms.NewRegistry(), hostsvc.NewRouter(localStubHost{}), store, "opencode")
	t.Cleanup(mgr.Stop)
	id, err := mgr.Add(t.Context(), addr, "tok", "Box")
	if err != nil {
		t.Fatal(err)
	}
	waitForRegister(t, mgr.registry, "r-abc123:opencode")
	mgr.RefreshInventories(context.Background())
	if got := mgr.resolveDir("/home/u/app"); got != "abc123" {
		t.Fatalf("precondition: resolveDir = %q, want abc123", got)
	}

	if err := mgr.Remove(t.Context(), id); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if got := mgr.resolveDir("/home/u/app"); got != "" {
		t.Errorf("resolveDir after Remove = %q; want local (stale inventory not evicted)", got)
	}
}

func TestDisplayNameFallbacks(t *testing.T) {
	if got := displayName(state.Remote{DisplayName: "Name"}); got != "Name" {
		t.Errorf("got %q", got)
	}
	if got := displayName(state.Remote{Hostname: "host"}); got != "host" {
		t.Errorf("got %q", got)
	}
	if got := displayName(state.Remote{Address: "a:1"}); got != "a:1" {
		t.Errorf("got %q", got)
	}
}

func TestManager_RefreshInventories(t *testing.T) {
	remoteReg := platforms.NewRegistry()
	remoteReg.Register(&fakePlatform{id: "opencode"})
	addr := startRealRemote(t, "tok", "abc123", remoteReg)

	store := newStateDB(t)
	mgr := NewManager(platforms.NewRegistry(), hostsvc.NewRouter(localStubHost{}), store, "opencode")
	t.Cleanup(mgr.Stop)
	ctx := context.Background()
	if _, err := mgr.Add(t.Context(), addr, "tok", "W"); err != nil {
		t.Fatal(err)
	}
	waitForRegister(t, mgr.registry, "r-abc123:opencode")

	mgr.RefreshInventories(ctx)
	// localStubHost.Projects returns /home/u/app -> the remote inventory
	// should now contain that dir under remote abc123.
	if got := mgr.resolveDir("/home/u/app"); got != "abc123" {
		t.Errorf("resolveDir after refresh = %q, want abc123", got)
	}
}

func newInvManager(t *testing.T) *Manager {
	t.Helper()
	m := NewManager(platforms.NewRegistry(), hostsvc.NewRouter(localStubHost{}), nil, "opencode")
	return m
}

func TestManager_ResolveTargets(t *testing.T) {
	m := newInvManager(t)
	// Seed remote inventory: remote "abc" has the github.com/org/repo project.
	m.invMu.Lock()
	m.inventory["abc"] = []ProjectIdentity{
		{Key: "github.com/org/repo", Origin: "https://github.com/org/repo", Dir: "/remote/repo"},
	}
	m.invMu.Unlock()

	local := []ProjectIdentity{
		{Key: "github.com/org/repo", Origin: "git@github.com:org/repo.git", Dir: "/local/repo"},
	}

	// Same origin on both -> two candidates (local + remote).
	cands := m.ResolveTargets("/local/repo", "git@github.com:org/repo.git", local)
	if len(cands) != 2 {
		t.Fatalf("expected 2 candidates, got %d: %+v", len(cands), cands)
	}
	var haveLocal, haveRemote bool
	for _, c := range cands {
		if c.RemoteID == "local" {
			haveLocal = true
		}
		if c.RemoteID == "abc" {
			haveRemote = true
			if c.Platform != "r-abc:opencode" {
				t.Errorf("remote candidate platform = %q", c.Platform)
			}
		}
	}
	if !haveLocal || !haveRemote {
		t.Fatalf("missing candidate: local=%v remote=%v", haveLocal, haveRemote)
	}

	// A project only the local has -> one candidate.
	one := m.ResolveTargets("/local/other", "git@github.com:org/other.git",
		[]ProjectIdentity{{Key: "github.com/org/other", Dir: "/local/other"}})
	if len(one) != 1 || one[0].RemoteID != "local" {
		t.Fatalf("expected 1 local candidate, got %+v", one)
	}

	// Unknown project -> zero candidates.
	none := m.ResolveTargets("/nowhere", "", nil)
	if len(none) != 0 {
		t.Fatalf("expected 0 candidates, got %+v", none)
	}
}

func TestManager_RemoteProjects(t *testing.T) {
	m := newInvManager(t)
	m.invMu.Lock()
	m.inventory["abc"] = []ProjectIdentity{
		{Key: "k1", Dir: "/remote/repo", SessionCount: 3, MessageCount: 12, LastUsed: 99, TotalTokensIn: 100, TotalTokensOut: 200},
		{Key: "k2", Dir: "/remote/other"},
	}
	m.invMu.Unlock()

	got := m.RemoteProjects()
	if len(got) != 2 {
		t.Fatalf("expected 2 remote projects, got %d: %+v", len(got), got)
	}
	byDir := map[string]db.ProjectStats{}
	for _, p := range got {
		byDir[p.Directory] = p
		if p.RemoteID != "abc" {
			t.Errorf("RemoteID = %q, want abc", p.RemoteID)
		}
		if p.Platform != "r-abc:opencode" {
			t.Errorf("Platform = %q, want r-abc:opencode", p.Platform)
		}
		// nameForRemoteID falls back to the id when not connected.
		if p.RemoteName != "abc" {
			t.Errorf("RemoteName = %q, want abc", p.RemoteName)
		}
	}
	repo, ok := byDir["/remote/repo"]
	if !ok || byDir["/remote/other"].Directory == "" {
		t.Fatalf("missing project dirs: %+v", byDir)
	}
	// Aggregate stats must survive the identity -> stats mapping.
	if repo.SessionCount != 3 || repo.MessageCount != 12 || repo.LastUsed != 99 ||
		repo.TotalTokensIn != 100 || repo.TotalTokensOut != 200 {
		t.Errorf("stats not carried over: %+v", repo)
	}

	// Empty inventory -> nil/empty slice.
	if got := newInvManager(t).RemoteProjects(); len(got) != 0 {
		t.Errorf("expected no remote projects, got %+v", got)
	}
}

func TestManager_ResolveDir(t *testing.T) {
	m := newInvManager(t)
	m.invMu.Lock()
	m.inventory["abc"] = []ProjectIdentity{{Key: "k", Dir: "/remote/repo"}}
	m.invMu.Unlock()

	if got := m.resolveDir("/remote/repo"); got != "abc" {
		t.Errorf("resolveDir(/remote/repo) = %q, want abc", got)
	}
	if got := m.resolveDir("/local/repo"); got != "" {
		t.Errorf("resolveDir(/local/repo) = %q, want local", got)
	}
}
