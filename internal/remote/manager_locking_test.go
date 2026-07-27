package remote

import (
	"sync"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

// connectedConn builds a RemoteConn that reports itself as connected
// without dialing, so manager code paths gated on health can run.
func connectedConn(remoteID, hostname string) *RemoteConn {
	c := NewRemoteConn("grpc://127.0.0.1:1", "token")
	c.health = HealthConnected
	c.remoteID = remoteID
	c.hostname = hostname
	return c
}

// TestEnabledRemotesDoesNotRecursivelyRLock pins the fact that
// EnabledRemotes must not take m.mu.RLock a second time while already
// holding it. Go's RWMutex is not reentrant: a Lock() arriving between
// the outer and inner RLock blocks the inner one forever, so the reader
// never releases and the writer never proceeds.
//
// The test hammers EnabledRemotes against updateName (a writer) and
// fails if the pair doesn't make progress.
func TestEnabledRemotesDoesNotRecursivelyRLock(t *testing.T) {
	m := &Manager{base: "opencode", remotes: map[int64]*managedRemote{
		1: {localID: 1, conn: connectedConn("remote-1", "host-1"), name: "one"},
		2: {localID: 2, conn: connectedConn("remote-2", "host-2"), name: "two"},
	}}

	done := make(chan struct{})
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 2000 {
				if got := m.EnabledRemotes(); len(got) != 2 {
					t.Errorf("EnabledRemotes() returned %d candidates, want 2", len(got))
					return
				}
			}
		}()
	}
	for i := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 2000 {
				m.updateName(int64(i%2+1), "renamed")
			}
		}()
	}
	go func() { wg.Wait(); close(done) }()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("EnabledRemotes deadlocked against a concurrent writer")
	}
}

// TestNameForRemoteIDLocked covers the extracted helper directly,
// including the hostname and unknown-id fallbacks.
func TestNameForRemoteIDLocked(t *testing.T) {
	m := &Manager{base: "opencode", remotes: map[int64]*managedRemote{
		1: {localID: 1, conn: connectedConn("remote-1", "host-1"), name: "one"},
		2: {localID: 2, conn: connectedConn("remote-2", "host-2")},
	}}
	tests := []struct{ remoteID, want string }{
		{"remote-1", "one"},
		{"remote-2", "host-2"},
		{"remote-3", "remote-3"},
	}
	for _, tt := range tests {
		if got := m.nameForRemoteID(tt.remoteID); got != tt.want {
			t.Errorf("nameForRemoteID(%q) = %q, want %q", tt.remoteID, got, tt.want)
		}
	}
}

// TestManagedRemoteAdapterFieldsAreLocked reproduces the managedRemote
// adapter-field race: the supervisor goroutine assigned and cleared
// mr.platform / mr.host with no lock while sessionCount and
// unregisterLocked read them under m.mu, so Stop()/disconnect() could
// observe a half-written adapter pair. Run under -race.
func TestManagedRemoteAdapterFieldsAreLocked(t *testing.T) {
	mr := &managedRemote{localID: 1, conn: connectedConn("remote-1", "host-1"), name: "one"}
	m := &Manager{
		base:     "opencode",
		registry: platforms.NewRegistry(),
		router:   hostsvc.NewRouter(nil),
		remotes:  map[int64]*managedRemote{1: mr},
	}

	var wg sync.WaitGroup
	// Supervisor: publish and tear down the adapters repeatedly.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 500 {
			platform := newRemotePlatform(mr.conn, m.base, func() string { return m.displayNameFor(1) })
			host := newRemoteHost(mr.conn)
			m.mu.Lock()
			mr.platform, mr.host = platform, host
			m.mu.Unlock()
			m.registry.Register(platform)
			m.unregisterAdapters(mr)
		}
	}()
	// Readers: the paths that observe the fields under m.mu.
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 500 {
				_ = m.sessionCount(1)
				_ = m.displayNameFor(1)
			}
		}()
	}
	wg.Wait()

	if got := m.sessionCount(1); got != 0 {
		t.Errorf("sessionCount after teardown = %d, want 0", got)
	}
}
