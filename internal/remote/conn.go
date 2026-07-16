package remote

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"

	pb "github.com/NoUseFreak/ocman/internal/remote/proto"
)

// Health is a RemoteConn's connection state (architecture Data Model).
type Health string

const (
	HealthConnecting   Health = "connecting"
	HealthConnected    Health = "connected"
	HealthOffline      Health = "offline"
	HealthAuthFailed   Health = "auth-failed"
	HealthIncompatible Health = "incompatible-version"
)

// RemoteConn owns one long-lived gRPC client connection to a remote
// ocman: dial, Hello handshake, health state, and the typed client.
// Commands are unary RPCs and event/project updates are server-streams
// over the same HTTP/2 connection (AD-4).
//
// It is shared by remotePlatform and remoteHost.
type RemoteConn struct {
	address string
	token   string

	mu              sync.RWMutex
	conn            *grpc.ClientConn
	client          pb.OcmanClient
	health          Health
	remoteID        string
	hostname        string
	protocolVersion int
	lastSeen        time.Time
}

// NewRemoteConn creates a RemoteConn for the given address and token.
// It does not dial; call Connect.
func NewRemoteConn(address, token string) *RemoteConn {
	return &RemoteConn{address: address, token: token, health: HealthConnecting}
}

// useTLS reports whether the configured address requests TLS.
func (c *RemoteConn) useTLS() bool {
	a := strings.ToLower(c.address)
	return strings.HasPrefix(a, "grpcs://") || strings.HasPrefix(a, "https://")
}

// trustedOverlay reports whether the address explicitly opts into
// plaintext gRPC on a private network.
func (c *RemoteConn) trustedOverlay() bool {
	return strings.HasPrefix(strings.ToLower(c.address), "grpc://")
}

// dialTarget strips a scheme prefix from the configured address for the
// gRPC dialer, which wants a bare host:port.
func dialTarget(address string) string {
	for _, p := range []string{"grpcs://", "grpc://", "https://", "http://"} {
		if strings.HasPrefix(strings.ToLower(address), p) {
			return address[len(p):]
		}
	}
	return address
}

// Connect dials the remote, runs Hello, and sets health accordingly.
// Returns an error on dial/handshake failure; health reflects the cause.
func (c *RemoteConn) Connect(ctx context.Context) error {
	c.setHealth(HealthConnecting)
	if !c.useTLS() && !c.trustedOverlay() {
		c.setHealth(HealthOffline)
		return fmt.Errorf("remote: address %q requires grpcs:// or explicit grpc:// trusted overlay", c.address)
	}

	// Drop any previous transport before redialing (reconnect path) so
	// we don't leak the old grpc.ClientConn.
	c.mu.Lock()
	old := c.conn
	c.conn = nil
	c.client = nil
	c.mu.Unlock()
	if old != nil {
		old.Close()
	}

	transport := insecure.NewCredentials()
	if c.useTLS() {
		transport = credentials.NewTLS(nil)
	}
	conn, err := grpc.NewClient(
		dialTarget(c.address),
		grpc.WithTransportCredentials(transport),
		grpc.WithPerRPCCredentials(bearerCreds{token: c.token, requireTLS: c.useTLS()}),
	)
	if err != nil {
		c.setHealth(HealthOffline)
		return fmt.Errorf("remote: dial %s: %w", c.address, err)
	}

	client := pb.NewOcmanClient(conn)
	hello, err := client.Hello(ctx, &pb.HelloReq{ProtocolVersion: ProtocolVersion})
	if err != nil {
		conn.Close()
		c.setHealth(classifyHandshakeError(err))
		return fmt.Errorf("remote: hello %s: %w", c.address, err)
	}
	if !protocolCompatible(hello.ProtocolVersion) {
		conn.Close()
		c.setHealth(HealthIncompatible)
		return fmt.Errorf("remote: incompatible protocol version %d (hub supports %d)", hello.ProtocolVersion, ProtocolVersion)
	}

	c.mu.Lock()
	c.conn = conn
	c.client = client
	c.health = HealthConnected
	c.remoteID = hello.InstanceId
	c.hostname = hello.Hostname
	c.protocolVersion = int(hello.ProtocolVersion)
	c.lastSeen = time.Now()
	c.mu.Unlock()
	return nil
}

// healthPingInterval is how often WaitForDrop probes a connected remote
// with a Hello to detect a restart. A restarted remote returns a new
// instance ID (or an error), both of which mean "reconnect".
var healthPingInterval = 5 * time.Second

// WaitForDrop blocks until the remote is no longer reachable at the same
// identity — either the transport leaves Ready or a periodic Hello probe
// fails or reports a different instance ID (a restart) — or ctx is
// cancelled. A lazily-idle grpc channel can look "not Ready" without being
// down, so the active probe is the authoritative signal; the transport
// watch just makes an outright disconnect react faster.
//
// After this returns the caller must re-run Connect to re-learn the
// (possibly changed) instance ID rather than trusting the channel.
func (c *RemoteConn) WaitForDrop(ctx context.Context) {
	c.mu.RLock()
	conn := c.conn
	client := c.client
	wantID := c.remoteID
	c.mu.RUnlock()
	if conn == nil || client == nil {
		return
	}

	ticker := time.NewTicker(healthPingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			hello, err := client.Hello(pctx, &pb.HelloReq{ProtocolVersion: ProtocolVersion})
			cancel()
			if err != nil {
				c.markOffline()
				return
			}
			if hello.InstanceId != wantID {
				// Remote restarted with a new identity.
				return
			}
			c.markSeen()
		}
	}
}

// Close tears down the connection.
func (c *RemoteConn) Close() {
	c.mu.Lock()
	conn := c.conn
	c.conn = nil
	c.client = nil
	c.mu.Unlock()
	if conn != nil {
		conn.Close()
	}
}

// Client returns the typed gRPC client, or nil if not connected.
func (c *RemoteConn) Client() pb.OcmanClient {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.client
}

// Health returns the current health state.
func (c *RemoteConn) Health() Health {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.health
}

// RemoteID returns the learned instance ID (empty until Hello succeeds).
func (c *RemoteConn) RemoteID() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.remoteID
}

// Hostname returns the remote's reported hostname.
func (c *RemoteConn) Hostname() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.hostname
}

// ProtocolVersion returns the remote's reported protocol version.
func (c *RemoteConn) ProtocolVersion() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.protocolVersion
}

// LastSeen returns the time of the last successful contact.
func (c *RemoteConn) LastSeen() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lastSeen
}

// markSeen updates lastSeen and ensures health is connected after a
// successful RPC.
func (c *RemoteConn) markSeen() {
	c.mu.Lock()
	c.lastSeen = time.Now()
	if c.health == HealthOffline {
		c.health = HealthConnected
	}
	c.mu.Unlock()
}

// markOffline transitions to offline after a transport error.
func (c *RemoteConn) markOffline() {
	c.setHealth(HealthOffline)
}

func (c *RemoteConn) setHealth(h Health) {
	c.mu.Lock()
	c.health = h
	c.mu.Unlock()
}

// protocolCompatible reports whether a remote's protocol version is in
// the hub's supported range. v1 requires an exact match (AD-12).
func protocolCompatible(v int32) bool { return v == ProtocolVersion }

// classifyHandshakeError maps a Hello error to a health state: an
// Unauthenticated code means a bad token; anything else is treated as a
// transport/offline condition.
func classifyHandshakeError(err error) Health {
	if codeIsUnauthenticated(err) {
		return HealthAuthFailed
	}
	return HealthOffline
}
