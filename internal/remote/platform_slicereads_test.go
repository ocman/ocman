package remote

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

// TestRemotePlatform_SliceReads_MapUnavailable is the regression test
// for the slice-read error-mapping gap: the hand-rolled slice reads
// (AgentCatalog, SlashCommands, ListPermissions, ListQuestions,
// PermissionRules) returned the raw gRPC Unavailable error instead of
// mapping it through remotePlatformError like jsonCall does, so
// callers checking errors.Is(err, platforms.ErrPlatformUnreachable)
// missed remote-down conditions on exactly these five methods.
func TestRemotePlatform_SliceReads_MapUnavailable(t *testing.T) {
	// Serve on a fixed port, connect, then stop the server so every
	// subsequent RPC fails with codes.Unavailable.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()

	reg := platforms.NewRegistry()
	reg.Register(&fakePlatform{id: "opencode"})
	stop := startRealRemoteAt(t, addr, "tok", "rid", reg)

	conn := NewRemoteConn(addr, "tok")
	if err := conn.Connect(context.Background()); err != nil {
		stop()
		t.Fatalf("connect: %v", err)
	}
	stop() // remote goes down; transport stays dialed

	rp := newRemotePlatform(conn, "opencode", func() string { return "Box" })
	ctx := context.Background()

	calls := map[string]func() error{
		"AgentCatalog":    func() error { _, err := rp.AgentCatalog(ctx, "s1"); return err },
		"SlashCommands":   func() error { _, err := rp.SlashCommands(ctx, "s1"); return err },
		"ListPermissions": func() error { _, err := rp.ListPermissions(ctx, "s1"); return err },
		"ListQuestions":   func() error { _, err := rp.ListQuestions(ctx, "s1"); return err },
		"PermissionRules": func() error { _, err := rp.PermissionRules(ctx, "s1"); return err },
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			err := call()
			if err == nil {
				t.Fatal("expected error from stopped remote")
			}
			if !errors.Is(err, platforms.ErrPlatformUnreachable) {
				t.Errorf("error not mapped to ErrPlatformUnreachable: %v", err)
			}
		})
	}
}
