package remote

import (
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

func TestNewListenerRejectsAccidentalPlaintext(t *testing.T) {
	cases := []struct {
		name string
		cfg  ListenConfig
	}{
		{"no TLS configuration", ListenConfig{Addr: "127.0.0.1:0", Token: "token"}},
		{"certificate without key", ListenConfig{Addr: "127.0.0.1:0", Token: "token", TLSCertFile: "cert.pem"}},
		{"key without certificate", ListenConfig{Addr: "127.0.0.1:0", Token: "token", TLSKeyFile: "key.pem"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			listener, err := NewListener(tc.cfg, NewServer(platforms.NewRegistry(), localStubHost{}, "id", "v"))
			if listener != nil {
				listener.Stop()
			}
			if err == nil {
				t.Fatal("expected insecure transport configuration to fail")
			}
		})
	}

	listener, err := NewListener(ListenConfig{
		Addr: "127.0.0.1:0", Token: "token", TrustedOverlay: true,
	}, NewServer(platforms.NewRegistry(), localStubHost{}, "id", "v"))
	if err != nil {
		t.Fatalf("explicit trusted overlay: %v", err)
	}
	listener.Stop()
}
