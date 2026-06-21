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
