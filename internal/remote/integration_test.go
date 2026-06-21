package remote

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

// startRealRemote spins a remote-side gRPC server on a real loopback port
// and returns its address so a RemoteConn can dial it like a real remote.
func startRealRemote(t *testing.T, token, instanceID string, reg *platforms.Registry) string {
	t.Helper()
	srv := NewServer(reg, localStubHost{}, instanceID, "v-test")
	ln, err := NewListener(ListenConfig{Addr: "127.0.0.1:0", Token: token}, srv)
	if err != nil {
		t.Fatalf("listener: %v", err)
	}
	go func() { _ = ln.Serve() }()
	t.Cleanup(ln.Stop)
	return ln.Addr()
}

func TestRemoteConn_ConnectAndHello(t *testing.T) {
	reg := platforms.NewRegistry()
	reg.Register(&fakePlatform{id: "opencode"})
	addr := startRealRemote(t, "tok", "remote-xyz", reg)

	conn := NewRemoteConn(addr, "tok")
	if err := conn.Connect(context.Background()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	if conn.Health() != HealthConnected {
		t.Fatalf("health = %q", conn.Health())
	}
	if conn.RemoteID() != "remote-xyz" {
		t.Fatalf("remoteID = %q", conn.RemoteID())
	}
}

func TestRemoteConn_AuthFailedHealth(t *testing.T) {
	reg := platforms.NewRegistry()
	reg.Register(&fakePlatform{id: "opencode"})
	addr := startRealRemote(t, "correct", "r", reg)

	conn := NewRemoteConn(addr, "wrong")
	err := conn.Connect(context.Background())
	if err == nil {
		t.Fatal("expected connect error with wrong token")
	}
	if conn.Health() != HealthAuthFailed {
		t.Fatalf("health = %q, want auth-failed", conn.Health())
	}
}

func TestManager_RegistersRemotePlatform(t *testing.T) {
	// Remote side.
	remoteReg := platforms.NewRegistry()
	remoteReg.Register(&fakePlatform{id: "opencode", sessions: []db.Session{
		{ID: "rs1", Platform: "opencode", Title: "Remote session"},
	}})
	addr := startRealRemote(t, "tok", "abc123", remoteReg)

	// Hub side: a real in-memory state.db with one saved remote.
	raw, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { raw.Close() })
	store, err := state.OpenFromSQL(raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddRemote(addr, "tok", "Workstation"); err != nil {
		t.Fatal(err)
	}

	hubReg := platforms.NewRegistry()
	router := hostsvc.NewRouter(localStubHost{})
	mgr := NewManager(hubReg, router, store, "opencode")

	mgr.Start(context.Background())

	// The connection happens in a goroutine; poll until the remote
	// platform appears in the hub registry.
	deadline := time.Now().Add(3 * time.Second)
	var rp platforms.Platform
	for time.Now().Before(deadline) {
		if p, ok := hubReg.Get(platforms.ID(CompoundPlatformID("abc123", "opencode"))); ok {
			rp = p
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if rp == nil {
		t.Fatal("remote platform never registered in hub registry")
	}

	// A remote session is visible and stamped with host identity.
	sessions, err := rp.Sessions(context.Background(), "", 0)
	if err != nil {
		t.Fatalf("sessions: %v", err)
	}
	if len(sessions) != 1 || sessions[0].ID != "rs1" {
		t.Fatalf("unexpected sessions: %+v", sessions)
	}
	if sessions[0].RemoteID != "abc123" || sessions[0].Platform != "r-abc123:opencode" {
		t.Fatalf("host stamping wrong: %+v", sessions[0])
	}

	// The remote host is registered in the router.
	if router.ForRemote("abc123").RemoteID() != "abc123" {
		t.Fatal("remote host not registered in router")
	}
}

func TestRemotePlatform_OfflineServesStale(t *testing.T) {
	reg := platforms.NewRegistry()
	reg.Register(&fakePlatform{id: "opencode", sessions: []db.Session{
		{ID: "s1", Platform: "opencode", Title: "live"},
	}})
	addr := startRealRemote(t, "tok", "rid", reg)

	conn := NewRemoteConn(addr, "tok")
	if err := conn.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	rp := newRemotePlatform(conn, "opencode", func() string { return "Box" })

	// First call populates the last-known cache from the live remote.
	live, err := rp.Sessions(context.Background(), "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(live) != 1 || live[0].Stale {
		t.Fatalf("live call should not be stale: %+v", live)
	}

	// Simulate the remote going offline.
	conn.Close()
	conn.markOffline()

	stale, err := rp.Sessions(context.Background(), "", 0)
	if err != nil {
		t.Fatalf("offline Sessions should not error: %v", err)
	}
	if len(stale) != 1 || !stale[0].Stale {
		t.Fatalf("offline call should yield stale rows: %+v", stale)
	}
}
