package remote

import (
	"context"
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
	pb "github.com/NoUseFreak/ocman/internal/remote/proto"
)

type unavailableHistory struct{}

func (unavailableHistory) ReadConversation(context.Context, platforms.Platform, string, int, int) (*platforms.SessionDetail, bool, error) {
	return nil, true, platforms.ErrHistoryUnavailable
}

type browsingPlatform struct{ *fakePlatform }

func (p browsingPlatform) Session(ctx context.Context, id string, limit, offset int) (*platforms.SessionDetail, error) {
	if d, handled, err := platforms.TryStoredConversation(ctx, p, id, limit, offset); handled || err != nil {
		return d, err
	}
	return p.fakePlatform.Session(ctx, id, limit, offset)
}

func TestOperationalSessionRPCDoesNotRequireHistory(t *testing.T) {
	reg := platforms.NewRegistry()
	reg.Register(browsingPlatform{&fakePlatform{id: "opencode"}})
	s := NewServer(reg, nil, "rid", "test")
	s.UseConversationContext(func(ctx context.Context) context.Context {
		return platforms.WithConversationReader(ctx, unavailableHistory{})
	})
	if _, err := s.Session(t.Context(), &pb.SessionReq{Platform: "opencode", SessionId: "s"}); err != nil {
		t.Fatalf("operational read blocked by history: %v", err)
	}
	if _, err := s.Session(t.Context(), &pb.SessionReq{Platform: "opencode", SessionId: "s", Browsing: true}); err == nil {
		t.Fatal("browsing RPC bypassed unavailable history")
	}
	ln, err := NewListener(ListenConfig{Addr: "127.0.0.1:0", Token: "tok", TrustedOverlay: true}, s)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = ln.Serve() }()
	t.Cleanup(ln.Stop)
	conn := NewRemoteConn("grpc://"+ln.Addr(), "tok")
	if err := conn.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(conn.Close)
	rp := newRemotePlatform(conn, "opencode", func() string { return "Box" })
	if _, err := rp.Session(t.Context(), "s", 1, 0); err != nil {
		t.Fatal(err)
	}
	ctx := platforms.WithConversationReader(t.Context(), unavailableHistory{})
	if _, err := rp.Session(ctx, "s", 1, 0); !errors.Is(err, platforms.ErrHistoryUnavailable) {
		t.Fatalf("browsing intent lost across RPC: %v", err)
	}
}
