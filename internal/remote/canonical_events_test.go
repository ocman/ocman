package remote

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/identity"
	"github.com/google/uuid"
)

func TestCanonicalPeerStreamRejectsNativeAndPendingReferences(t *testing.T) {
	id := "ocm_" + uuid.NewString()
	for _, ref := range []string{id, "native", identity.PendingReference(), "ocm_invalid"} {
		t.Run(ref, func(t *testing.T) {
			frame := fmt.Sprintf("event: permission.v2.asked\ndata: {\"sessionID\":%q,\"id\":\"native-permission\",\"metadata\":{\"sessionID\":\"opaque-native\"}}\n\n", ref)
			registry := platforms.NewRegistry()
			registry.Register(&canonicalRPCPlatform{fakePlatform: &fakePlatform{id: "opencode", events: []string{frame}}})
			if err := registry.EnableCanonicalSessions(); err != nil {
				t.Fatal(err)
			}
			server := NewServer(registry, localStubHost{}, "owner", "test")
			listener, err := NewListener(ListenConfig{Addr: "127.0.0.1:0", Token: "test-token", TrustedOverlay: true}, server)
			if err != nil {
				t.Fatal(err)
			}
			go func() { _ = listener.Serve() }()
			t.Cleanup(listener.Stop)
			conn := NewRemoteConn("grpc://"+listener.Addr(), "test-token")
			if err := conn.Connect(t.Context()); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(conn.Close)
			conn.mu.Lock()
			conn.protocolVersion = canonicalSessionProtocol
			conn.mu.Unlock()
			remote := newRemotePlatform(conn, "opencode", func() string { return "owner" })
			var out bytes.Buffer
			err = remote.ProxyEvents(t.Context(), id, &out, func() {})
			if ref == id {
				if err != nil || !strings.Contains(out.String(), id) || !strings.Contains(out.String(), "native-permission") || !strings.Contains(out.String(), "opaque-native") {
					t.Fatalf("canonical frame lost correlation or opaque metadata: %q %v", out.String(), err)
				}
			} else if !errors.Is(err, platforms.ErrPlatformUnreachable) || out.Len() != 0 {
				t.Fatalf("invalid peer frame reached caller: %q %v", out.String(), err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if err := remote.ProxyEvents(ctx, id, &bytes.Buffer{}, func() {}); !errors.Is(err, context.Canceled) {
				t.Fatalf("stream cancellation changed: %v", err)
			}
		})
	}
}
