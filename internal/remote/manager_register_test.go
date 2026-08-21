package remote

import (
	"context"
	"database/sql"
	"sync"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

// TestManager_DoesNotRegisterAdaptersForAnUnmanagedRemote reproduces the
// stale-adapter leak: the supervisor published and registered a connected
// remote's adapters without rechecking ownership, so a disconnect landing
// between "connected" and "registered" removed the manager entry first
// and the supervisor then registered adapters nothing would ever remove.
//
// The supervisor is parked right before registration, the remote is
// removed, and the supervisor resumed; afterwards neither the registry
// nor the router may hold an entry for it.
func TestManager_DoesNotRegisterAdaptersForAnUnmanagedRemote(t *testing.T) {
	remoteReg := platforms.NewRegistry()
	remoteReg.Register(&fakePlatform{id: "opencode"})
	addr := startRealRemote(t, "tok", "inst-gone", remoteReg)

	raw, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { raw.Close() })
	store, err := state.OpenFromSQL(raw)
	if err != nil {
		t.Fatal(err)
	}
	localID, err := store.AddRemote(addr, "tok", "Box")
	if err != nil {
		t.Fatal(err)
	}

	hubReg := platforms.NewRegistry()
	router := hostsvc.NewRouter(localStubHost{})
	mgr := NewManager(hubReg, router, store, "opencode")

	parked := make(chan struct{})
	resume := make(chan struct{})
	var once sync.Once
	mgr.beforeAdapterRegister = func() {
		once.Do(func() {
			close(parked)
			<-resume
		})
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr.Start(ctx)

	// The supervisor connected and is about to register its adapters.
	<-parked

	// Meanwhile the user removes the remote: it is no longer managed, so
	// nothing is left to tear the adapters down afterwards.
	if err := mgr.Remove(localID); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	close(resume)

	// Stop waits for the supervisor goroutine to finish, then tears down
	// whatever is still managed — which is nothing, so anything the
	// supervisor registered after the removal survives as a leak.
	mgr.Stop()

	if _, ok := hubReg.Get(platforms.ID(CompoundPlatformID("inst-gone", "opencode"))); ok {
		t.Error("stale platform registered for a removed remote")
	}
	if got := router.Remotes(); len(got) != 0 {
		t.Errorf("stale router hosts for a removed remote: %v", got)
	}
}

// TestPublishAdapters_DisconnectAfterwardsTearsDownExactlyWhatWasPublished
// covers the other order in the same window. Publication and registration
// happen in one critical section, so a disconnect can only land wholly
// before (superseded, nothing registered — covered below) or wholly after,
// where it must find both entries and remove them.
func TestPublishAdapters_DisconnectAfterwardsTearsDownExactlyWhatWasPublished(t *testing.T) {
	reg := platforms.NewRegistry()
	router := hostsvc.NewRouter(localStubHost{})
	mr := &managedRemote{localID: 1, conn: connectedConn("remote-1", "host-1"), name: "one"}
	m := &Manager{
		base:      "opencode",
		registry:  reg,
		router:    router,
		remotes:   map[int64]*managedRemote{1: mr},
		inventory: map[string][]ProjectIdentity{},
	}
	platform := newRemotePlatform(mr.conn, m.base, func() string { return m.displayNameFor(1) })

	if !m.publishAdapters(1, mr, platform, newRemoteHost(mr.conn)) {
		t.Fatal("publishAdapters reported failure for the managed remote")
	}
	if _, ok := reg.Get(platform.ID()); !ok {
		t.Fatal("platform not registered for the managed remote")
	}
	if _, ok := router.Remotes()["remote-1"]; !ok {
		t.Fatal("host not registered for the managed remote")
	}

	m.disconnect(1)

	if _, ok := reg.Get(platform.ID()); ok {
		t.Error("platform survived the disconnect")
	}
	if got := router.Remotes(); len(got) != 0 {
		t.Errorf("router host survived the disconnect: %v", got)
	}
}

// TestPublishAdapters_SkipsASupersededRemote covers the pre-registration
// ownership check on its own: a managed remote replaced while its
// supervisor was connecting must not register anything.
func TestPublishAdapters_SkipsASupersededRemote(t *testing.T) {
	reg := platforms.NewRegistry()
	router := hostsvc.NewRouter(localStubHost{})
	old := &managedRemote{localID: 1, conn: connectedConn("remote-1", "host-1")}
	current := &managedRemote{localID: 1, conn: connectedConn("remote-1", "host-1")}
	m := &Manager{
		base:      "opencode",
		registry:  reg,
		router:    router,
		remotes:   map[int64]*managedRemote{1: current},
		inventory: map[string][]ProjectIdentity{},
	}
	platform := newRemotePlatform(old.conn, m.base, func() string { return m.displayNameFor(1) })

	if m.publishAdapters(1, old, platform, newRemoteHost(old.conn)) {
		t.Fatal("publishAdapters reported success for a superseded remote")
	}
	if _, ok := reg.Get(platform.ID()); ok {
		t.Error("superseded remote registered a platform")
	}
	if got := router.Remotes(); len(got) != 0 {
		t.Errorf("superseded remote registered a host: %v", got)
	}
	if old.platform != nil || old.host != nil {
		t.Error("superseded remote published its adapter fields")
	}
}

// TestPublishAdapters_SkipsARemoteWithoutAnInstanceID pins the symmetry
// between the two registrations: a handshake that yielded no remote ID
// gives the router no key, so the platform must not be registered either
// — an "r-:opencode" adapter nothing can address is a leak.
func TestPublishAdapters_SkipsARemoteWithoutAnInstanceID(t *testing.T) {
	reg := platforms.NewRegistry()
	router := hostsvc.NewRouter(localStubHost{})
	mr := &managedRemote{localID: 1, conn: connectedConn("", "host-1")}
	m := &Manager{
		base:      "opencode",
		registry:  reg,
		router:    router,
		remotes:   map[int64]*managedRemote{1: mr},
		inventory: map[string][]ProjectIdentity{},
	}
	platform := newRemotePlatform(mr.conn, m.base, func() string { return m.displayNameFor(1) })

	if m.publishAdapters(1, mr, platform, newRemoteHost(mr.conn)) {
		t.Fatal("publishAdapters reported success for a remote with no instance ID")
	}
	if _, ok := reg.Get(platform.ID()); ok {
		t.Errorf("registered an unaddressable platform %q", platform.ID())
	}
	if got := router.Remotes(); len(got) != 0 {
		t.Errorf("registered a host without a remote ID: %v", got)
	}
	if mr.platform != nil || mr.host != nil {
		t.Error("published adapter fields for a remote with no instance ID")
	}
}

func TestPublishAdapters_RejectsDuplicateRemoteIdentity(t *testing.T) {
	reg := platforms.NewRegistry()
	router := hostsvc.NewRouter(localStubHost{})
	first := &managedRemote{localID: 1, conn: connectedConn("shared-id", "host-1")}
	second := &managedRemote{localID: 2, conn: connectedConn("shared-id", "host-2")}
	m := &Manager{
		base:      "opencode",
		registry:  reg,
		router:    router,
		remotes:   map[int64]*managedRemote{1: first, 2: second},
		inventory: map[string][]ProjectIdentity{},
	}
	firstPlatform := newRemotePlatform(first.conn, m.base, func() string { return "first" })
	firstHost := newRemoteHost(first.conn)
	if !m.publishAdapters(1, first, firstPlatform, firstHost) {
		t.Fatal("first remote was not published")
	}
	secondPlatform := newRemotePlatform(second.conn, m.base, func() string { return "second" })
	if m.publishAdapters(2, second, secondPlatform, newRemoteHost(second.conn)) {
		t.Fatal("duplicate remote identity was published")
	}

	registered, ok := reg.Get(firstPlatform.ID())
	if !ok || registered != firstPlatform {
		t.Fatal("duplicate registration replaced the original platform")
	}
	routed, ok := router.LookupRemote("shared-id")
	if !ok || routed != firstHost {
		t.Fatal("duplicate registration replaced the original host")
	}
	if second.platform != nil || second.host != nil {
		t.Fatal("rejected remote retained published adapters")
	}
	m.unregisterAdapters(second)
	if registered, ok := reg.Get(firstPlatform.ID()); !ok || registered != firstPlatform {
		t.Fatal("tearing down rejected remote removed the original platform")
	}
}

// TestSupersededSupervisorPersistsTheConnectItMade pins that a supervisor
// which connected but was superseded before publishing still writes the
// outcome back to state.db. Returning early without persistHealth left
// the row showing the pre-connect health for a remote that demonstrably
// did connect.
func TestSupersededSupervisorPersistsTheConnectItMade(t *testing.T) {
	remoteReg := platforms.NewRegistry()
	remoteReg.Register(&fakePlatform{id: "opencode"})
	addr := startRealRemote(t, "tok", "inst-super", remoteReg)

	raw, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { raw.Close() })
	store, err := state.OpenFromSQL(raw)
	if err != nil {
		t.Fatal(err)
	}
	localID, err := store.AddRemote(addr, "tok", "Box")
	if err != nil {
		t.Fatal(err)
	}

	mgr := NewManager(platforms.NewRegistry(), hostsvc.NewRouter(localStubHost{}), store, "opencode")
	parked := make(chan struct{})
	resume := make(chan struct{})
	var once sync.Once
	mgr.beforeAdapterRegister = func() {
		once.Do(func() {
			close(parked)
			<-resume
		})
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr.Start(ctx)
	<-parked

	// Supersede the parked supervisor without re-dialling, so nothing
	// else can write the health row.
	mgr.disconnect(localID)
	close(resume)
	mgr.Stop()

	r, err := store.GetRemote(localID)
	if err != nil {
		t.Fatalf("GetRemote: %v", err)
	}
	if r.LastHealth != string(HealthConnected) {
		t.Errorf("persisted health = %q; want %q", r.LastHealth, HealthConnected)
	}
	if r.RemoteID != "inst-super" {
		t.Errorf("persisted remote id = %q; want the id we handshook", r.RemoteID)
	}
}
