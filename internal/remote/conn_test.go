package remote

import (
	"context"
	"testing"
)

func TestUseTLSAndDialTarget(t *testing.T) {
	cases := []struct {
		addr     string
		wantTLS  bool
		wantDial string
	}{
		{"ws.local:8230", false, "ws.local:8230"},
		{"grpc://ws.local:8230", false, "ws.local:8230"},
		{"grpcs://ws.local:8230", true, "ws.local:8230"},
		{"https://ws.local:8230", true, "ws.local:8230"},
		{"http://ws.local:8230", false, "ws.local:8230"},
	}
	for _, c := range cases {
		conn := NewRemoteConn(c.addr, "tok")
		if got := conn.useTLS(); got != c.wantTLS {
			t.Errorf("useTLS(%q) = %v, want %v", c.addr, got, c.wantTLS)
		}
		if got := dialTarget(c.addr); got != c.wantDial {
			t.Errorf("dialTarget(%q) = %q, want %q", c.addr, got, c.wantDial)
		}
	}
}

func TestRemoteConnRequiresExplicitPlaintextOptIn(t *testing.T) {
	bare := NewRemoteConn("ws.local:8230", "tok")
	if bare.trustedOverlay() {
		t.Fatal("bare address must not opt into plaintext")
	}
	if err := bare.Connect(context.Background()); err == nil {
		t.Fatal("bare address must be rejected before dialing")
	}
	if !NewRemoteConn("grpc://ws.local:8230", "tok").trustedOverlay() {
		t.Fatal("grpc scheme must explicitly opt into trusted-overlay plaintext")
	}
}

func TestProtocolCompatible(t *testing.T) {
	if ProtocolVersion != 5 {
		t.Fatalf("ProtocolVersion = %d, want 5", ProtocolVersion)
	}
	if !protocolCompatible(ProtocolVersion) {
		t.Error("current protocol version should be compatible")
	}
	for _, version := range []int32{ProtocolVersion - 1, ProtocolVersion + 1} {
		if protocolCompatible(version) {
			t.Errorf("protocol version %d must be incompatible (exact match)", version)
		}
	}
}
