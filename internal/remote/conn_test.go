package remote

import "testing"

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

func TestProtocolCompatible(t *testing.T) {
	if !protocolCompatible(ProtocolVersion) {
		t.Error("current protocol version should be compatible")
	}
	if protocolCompatible(ProtocolVersion + 1) {
		t.Error("a different version must be incompatible (v1 exact match)")
	}
}
