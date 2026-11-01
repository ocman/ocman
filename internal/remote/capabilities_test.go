package remote

import (
	"context"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platforms"
)

// hungCapsPlatform answers every capability probe only once release closes,
// standing in for a half-open connection whose RPCs never return.
type hungCapsPlatform struct {
	fakePlatform
	release chan struct{}
}

func (p *hungCapsPlatform) Capabilities() platforms.Capabilities {
	<-p.release
	return p.fakePlatform.Capabilities()
}

type hungCapsHost struct {
	localStubHost
	release chan struct{}
}

func (h hungCapsHost) Capabilities() hostsvc.HostCaps {
	<-h.release
	return h.localStubHost.Capabilities()
}

// startHungCapsRemote serves a remote whose platform and host capability
// probes block until the test ends, and returns a connected RemoteConn.
func startHungCapsRemote(t *testing.T) *RemoteConn {
	t.Helper()
	release := make(chan struct{})
	reg := platforms.NewRegistry()
	reg.Register(&hungCapsPlatform{fakePlatform: fakePlatform{id: "opencode"}, release: release})
	srv := NewServer(reg, hungCapsHost{release: release}, "rid", "v-test")
	ln, err := NewListener(ListenConfig{Addr: "127.0.0.1:0", Token: "tok", TrustedOverlay: true}, srv)
	if err != nil {
		t.Fatalf("listener: %v", err)
	}
	go func() { _ = ln.Serve() }()
	t.Cleanup(ln.Stop)
	// Registered after ln.Stop so it runs first and unblocks the handlers.
	t.Cleanup(func() { close(release) })
	conn := NewRemoteConn("grpc://"+ln.Addr(), "tok")
	if err := conn.Connect(context.Background()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(conn.Close)
	return conn
}

// returnsWithin fails the test when fn has not returned within d.
func returnsWithin(t *testing.T, d time.Duration, name string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("%s did not return within %s from a hung remote", name, d)
	}
}

// TestRemoteCapabilities_HungRemoteIsBounded is a regression test: request
// paths such as GET /api/capabilities call Capabilities() on every remote
// adapter, and a remote that never answers must not block them forever.
func TestRemoteCapabilities_HungRemoteIsBounded(t *testing.T) {
	conn := startHungCapsRemote(t)
	rp := newRemotePlatform(conn, "opencode", func() string { return "Box" })
	rh := newRemoteHost(conn)

	var platformCaps platforms.Capabilities
	returnsWithin(t, 10*time.Second, "remotePlatform.Capabilities", func() { platformCaps = rp.Capabilities() })
	if platformCaps.Composer {
		t.Errorf("hung platform capabilities = %+v, want zero", platformCaps)
	}
	var hostCaps hostsvc.HostCaps
	returnsWithin(t, 10*time.Second, "remoteHost.Capabilities", func() { hostCaps = rh.Capabilities() })
	if hostCaps.GitDiff {
		t.Errorf("hung host capabilities = %+v, want zero", hostCaps)
	}
}

// TestPrefetchCapabilities_CachesOnConnection checks that capabilities read
// at connect time are served from the adapters without another round trip.
func TestPrefetchCapabilities_CachesOnConnection(t *testing.T) {
	conn := connectedPair(t, &fakePlatform{id: "opencode"})
	rp := newRemotePlatform(conn, "opencode", func() string { return "Box" })
	rh := newRemoteHost(conn)
	prefetchCapabilities(t.Context(), 1, []*remotePlatform{rp}, rh)

	conn.Close() // any further fetch would now fail closed
	if caps := rp.Capabilities(); !caps.Composer || !caps.Revert {
		t.Errorf("prefetched platform capabilities = %+v", caps)
	}
	if caps := rh.Capabilities(); !caps.GitDiff {
		t.Errorf("prefetched host capabilities = %+v", caps)
	}
}

// TestPrefetchCapabilities_FailureIsNotCached checks that a failed prefetch
// returns within its bound and leaves the adapters to retry on first use.
func TestPrefetchCapabilities_FailureIsNotCached(t *testing.T) {
	prev := capabilitiesTimeout
	capabilitiesTimeout = 50 * time.Millisecond
	t.Cleanup(func() { capabilitiesTimeout = prev })

	conn := startHungCapsRemote(t)
	rp := newRemotePlatform(conn, "opencode", func() string { return "Box" })
	rh := newRemoteHost(conn)
	returnsWithin(t, 5*time.Second, "prefetchCapabilities", func() {
		prefetchCapabilities(t.Context(), 1, []*remotePlatform{rp}, rh)
	})
	rp.mu.RLock()
	platformSet := rp.capsSet
	rp.mu.RUnlock()
	rh.mu.RLock()
	hostSet := rh.capsSet
	rh.mu.RUnlock()
	if platformSet || hostSet {
		t.Errorf("failed prefetch cached capabilities: platform=%v host=%v", platformSet, hostSet)
	}
}
