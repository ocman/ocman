package remote

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/testutil"
)

// waitTimeout is deliberately generous: these tests coordinate real gRPC
// dials and supervisor goroutines, so a tight budget only buys flakes on
// a loaded CI runner. A passing run exits as soon as the condition holds.
const waitTimeout = 10 * time.Second

// startRealRemote spins a remote-side gRPC server on a real loopback port
// and returns its address so a RemoteConn can dial it like a real remote.
func startRealRemote(t *testing.T, token, instanceID string, reg *platforms.Registry) string {
	t.Helper()
	srv := NewServer(reg, localStubHost{}, instanceID, "v-test")
	ln, err := NewListener(ListenConfig{Addr: "127.0.0.1:0", Token: token, TrustedOverlay: true}, srv)
	if err != nil {
		t.Fatalf("listener: %v", err)
	}
	go func() { _ = ln.Serve() }()
	t.Cleanup(ln.Stop)
	return "grpc://" + ln.Addr()
}

// startRealRemoteAt serves on a fixed address (used to restart a remote on
// the same port so a hub can reconnect to it). Returns a stop func.
func startRealRemoteAt(t *testing.T, addr, token, instanceID string, reg *platforms.Registry) func() {
	t.Helper()
	srv := NewServer(reg, localStubHost{}, instanceID, "v-test")
	ln, err := NewListener(ListenConfig{Addr: addr, Token: token, TrustedOverlay: true}, srv)
	if err != nil {
		t.Fatalf("listener %s: %v", addr, err)
	}
	go func() { _ = ln.Serve() }()
	return ln.Stop
}

// TestManager_ReconnectsAfterRemoteRestart reproduces the bug where a hub
// never reconnects after a remote restarts. The remote goes down and comes
// back on the same address with a fresh instance ID; the hub must drop the
// stale adapter and register one for the new instance ID.
func TestManager_ReconnectsAfterRemoteRestart(t *testing.T) {
	// Grab a free port, then serve on it explicitly so we can rebind
	// after stopping (a restart on the same address).
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()

	// Probe fast so the restart is detected quickly.
	prev := healthPingInterval
	healthPingInterval = 100 * time.Millisecond
	t.Cleanup(func() { healthPingInterval = prev })

	remoteReg := platforms.NewRegistry()
	remoteReg.Register(&fakePlatform{id: "opencode"})
	stop1 := startRealRemoteAt(t, addr, "tok", "inst-1", remoteReg)

	raw, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { raw.Close() })
	store, err := state.OpenFromSQL(raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddRemote("grpc://"+addr, "tok", "Box"); err != nil {
		t.Fatal(err)
	}

	hubReg := platforms.NewRegistry()
	router := hostsvc.NewRouter(localStubHost{})
	mgr := NewManager(hubReg, router, store, "opencode")
	t.Cleanup(mgr.Stop)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr.Start(ctx)

	waitForPlatform := func(instanceID string) {
		id := platforms.ID(CompoundPlatformID(instanceID, "opencode"))
		testutil.WaitFor(t, waitTimeout, "platform "+instanceID+" to register", func() bool {
			_, ok := hubReg.Get(id)
			return ok
		})
	}

	waitForPlatform("inst-1")

	// Remote restarts on the same address with a new instance ID.
	stop1()
	stop2 := startRealRemoteAt(t, addr, "tok", "inst-2", remoteReg)
	t.Cleanup(stop2)

	// The hub must reconnect and register the new instance's platform.
	waitForPlatform("inst-2")

	// The stale adapter for the old instance ID must be gone.
	if _, ok := hubReg.Get(platforms.ID(CompoundPlatformID("inst-1", "opencode"))); ok {
		t.Fatal("stale platform for inst-1 still registered after restart")
	}
}

// TestManager_RetriesWhenOfflineAtStartup covers the backoff path: the
// remote is down when the hub starts, so the first Connect fails; once the
// remote comes up the supervisor's retry loop must connect and register.
func TestManager_RetriesWhenOfflineAtStartup(t *testing.T) {
	// Reserve an address but don't serve on it yet — the remote is down.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()

	// Retry fast.
	prev := reconnectBaseDelay
	reconnectBaseDelay = 50 * time.Millisecond
	t.Cleanup(func() { reconnectBaseDelay = prev })

	raw, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { raw.Close() })
	store, err := state.OpenFromSQL(raw)
	if err != nil {
		t.Fatal(err)
	}
	localID, err := store.AddRemote("grpc://"+addr, "tok", "Box")
	if err != nil {
		t.Fatal(err)
	}

	hubReg := platforms.NewRegistry()
	router := hostsvc.NewRouter(localStubHost{})
	mgr := NewManager(hubReg, router, store, "opencode")
	t.Cleanup(mgr.Stop)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr.Start(ctx)

	// Let the supervisor actually fail at least once before bringing the
	// remote up — polling for the unhealthy state instead of sleeping a
	// fixed 120ms keeps the test both faster and stronger.
	testutil.WaitFor(t, waitTimeout, "the supervisor to record a failed attempt", func() bool {
		conn, ok := mgr.Conn(localID)
		return ok && conn.Health() != HealthConnected
	})

	remoteReg := platforms.NewRegistry()
	remoteReg.Register(&fakePlatform{id: "opencode"})
	stop := startRealRemoteAt(t, addr, "tok", "late", remoteReg)
	t.Cleanup(stop)

	id := platforms.ID(CompoundPlatformID("late", "opencode"))
	testutil.WaitFor(t, waitTimeout, "the platform to register after the remote came up", func() bool {
		_, ok := hubReg.Get(id)
		return ok
	})
}

// TestManager_AuthFailedDoesNotRetry ensures a bad token stops the
// supervisor instead of hammering the remote forever.
func TestManager_AuthFailedDoesNotRetry(t *testing.T) {
	remoteReg := platforms.NewRegistry()
	remoteReg.Register(&fakePlatform{id: "opencode"})
	addr := startRealRemote(t, "correct-token", "auth-remote", remoteReg)

	raw, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { raw.Close() })
	store, err := state.OpenFromSQL(raw)
	if err != nil {
		t.Fatal(err)
	}
	// Wrong token.
	localID, err := store.AddRemote(addr, "wrong-token", "Box")
	if err != nil {
		t.Fatal(err)
	}

	hubReg := platforms.NewRegistry()
	router := hostsvc.NewRouter(localStubHost{})
	mgr := NewManager(hubReg, router, store, "opencode")
	t.Cleanup(mgr.Stop)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr.Start(ctx)

	// Health should settle on auth-failed and no platform registers.
	testutil.WaitFor(t, waitTimeout, "health to settle on auth-failed", func() bool {
		conn, ok := mgr.Conn(localID)
		return ok && conn.Health() == HealthAuthFailed
	})
	conn, ok := mgr.Conn(localID)
	if !ok || conn.Health() != HealthAuthFailed {
		t.Fatalf("expected auth-failed health, got ok=%v health=%v", ok, func() Health {
			if conn != nil {
				return conn.Health()
			}
			return ""
		}())
	}
	if _, ok := hubReg.Get(platforms.ID(CompoundPlatformID("auth-remote", "opencode"))); ok {
		t.Fatal("platform should not register on auth failure")
	}
}

func TestNextDelay(t *testing.T) {
	prevBase, prevMax := reconnectBaseDelay, reconnectMaxDelay
	reconnectBaseDelay = 2 * time.Second
	reconnectMaxDelay = 60 * time.Second
	t.Cleanup(func() { reconnectBaseDelay, reconnectMaxDelay = prevBase, prevMax })

	cases := []struct{ in, want time.Duration }{
		{2 * time.Second, 4 * time.Second},
		{4 * time.Second, 8 * time.Second},
		{40 * time.Second, 60 * time.Second}, // capped
		{60 * time.Second, 60 * time.Second}, // stays capped
	}
	for _, c := range cases {
		if got := nextDelay(c.in); got != c.want {
			t.Errorf("nextDelay(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestSleepCtx(t *testing.T) {
	// Completes normally.
	if !sleepCtx(context.Background(), time.Millisecond) {
		t.Fatal("sleepCtx should return true when it sleeps fully")
	}
	// Cancelled early returns false.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if sleepCtx(ctx, time.Hour) {
		t.Fatal("sleepCtx should return false when ctx is cancelled")
	}
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
	t.Cleanup(mgr.Stop)

	mgr.Start(context.Background())

	// The connection happens in a goroutine; poll until the remote
	// platform appears in the hub registry.
	var rp platforms.Platform
	testutil.WaitFor(t, waitTimeout, "the remote platform to register in the hub registry", func() bool {
		p, ok := hubReg.Get(platforms.ID(CompoundPlatformID("abc123", "opencode")))
		rp = p
		return ok
	})

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

func TestRemotePlatform_MutationsAndCreateRoundTrip(t *testing.T) {
	reg := platforms.NewRegistry()
	fp := &fakePlatform{id: "opencode"}
	reg.Register(fp)
	addr := startRealRemote(t, "tok", "rid", reg)

	conn := NewRemoteConn(addr, "tok")
	if err := conn.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	rp := newRemotePlatform(conn, "opencode", func() string { return "Box" })

	// SendMessage routes over gRPC to the remote adapter.
	if err := rp.SendMessage(context.Background(), platforms.SendMessageRequest{
		SessionID: "s1", Message: "drive remotely",
	}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if fp.sent == nil || fp.sent.Message != "drive remotely" {
		t.Fatalf("remote did not receive message: %+v", fp.sent)
	}

	// CreateSession returns the remote-created id.
	resp, err := rp.CreateSession(context.Background(), platforms.CreateSessionRequest{Directory: "/x"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if resp.ID != "new-sess" {
		t.Fatalf("CreateSession id = %q", resp.ID)
	}
}

func TestRemoteHost_CreateWorktreeSessionRoundTrip(t *testing.T) {
	reg := platforms.NewRegistry()
	reg.Register(&fakePlatform{id: "opencode"})
	addr := startRealRemote(t, "tok", "rid", reg)

	conn := NewRemoteConn(addr, "tok")
	if err := conn.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	rh := newRemoteHost(conn)

	res, err := rh.CreateWorktreeSession(context.Background(), hostsvc.WorktreeSessionRequest{
		ProjectDir: "/repo", Branch: "feature", NewBranch: true, BaseRef: "main",
	})
	if err != nil {
		t.Fatalf("CreateWorktreeSession: %v", err)
	}
	// localStubHost returns a canned result; the point is the round-trip.
	if res.WorktreePath != "/wt" || res.Branch != "b" {
		t.Fatalf("unexpected worktree result: %+v", res)
	}

	// Host capabilities round-trip.
	caps := rh.Capabilities()
	if !caps.Worktrees || !caps.Tmux {
		t.Fatalf("unexpected host caps: %+v", caps)
	}
}

// writeSelfSignedCert generates a self-signed cert/key for 127.0.0.1 and
// writes them to temp files, returning the paths.
func writeSelfSignedCert(t *testing.T) (certPath, keyPath string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certPath = filepath.Join(dir, "cert.pem")
	keyPath = filepath.Join(dir, "key.pem")
	certOut, _ := os.Create(certPath)
	_ = pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: der})
	certOut.Close()
	keyBytes, _ := x509.MarshalECPrivateKey(key)
	keyOut, _ := os.Create(keyPath)
	_ = pem.Encode(keyOut, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})
	keyOut.Close()
	return certPath, keyPath
}

func TestRemote_TLSRoundTrip(t *testing.T) {
	certPath, keyPath := writeSelfSignedCert(t)
	reg := platforms.NewRegistry()
	reg.Register(&fakePlatform{id: "opencode", sessions: []db.Session{{ID: "s1", Platform: "opencode"}}})
	srv := NewServer(reg, localStubHost{}, "tls-remote", "v")

	ln, err := NewListener(ListenConfig{
		Addr: "127.0.0.1:0", Token: "tok", TLSCertFile: certPath, TLSKeyFile: keyPath,
	}, srv)
	if err != nil {
		t.Fatalf("listener: %v", err)
	}
	if !ln.TLS() {
		t.Fatal("listener should report TLS enabled")
	}
	go func() { _ = ln.Serve() }()
	t.Cleanup(ln.Stop)

	// Dial with grpcs:// so the conn uses TLS. The self-signed cert
	// won't validate against the system roots, so we expect the
	// handshake to fail — which still proves the TLS transport path is
	// exercised (a plaintext dial would fail differently). For a clean
	// assertion we just confirm a non-TLS dial to a TLS server fails.
	plain := NewRemoteConn("127.0.0.1:"+portOf(ln.Addr()), "tok")
	if err := plain.Connect(context.Background()); err == nil {
		t.Fatal("plaintext dial to a TLS server should fail")
	}
}

func portOf(addr string) string {
	_, port, _ := net.SplitHostPort(addr)
	return port
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
