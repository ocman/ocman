package remote

import (
	"context"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type refreshingPlatform struct {
	fakePlatform
}

func (p *refreshingPlatform) RefreshPermissions(context.Context, string) ([]platforms.LivePrompt, error) {
	return []platforms.LivePrompt{{"id": "live"}}, nil
}

// The hub must get the owner's authoritative list, and an owner without
// one must fail instead of serving its cache: absence means "resolved".
func TestRemotePlatform_RefreshPermissions(t *testing.T) {
	ctx := context.Background()

	conn := connectedPair(t, &fakePlatform{id: "opencode"})
	rp := newRemotePlatform(conn, "opencode", func() string { return "Box" })
	if _, err := rp.RefreshPermissions(ctx, "s1"); status.Code(err) != codes.Unimplemented {
		t.Fatalf("cache-only owner: err = %v, want Unimplemented", err)
	}

	reg := platforms.NewRegistry()
	reg.Register(&refreshingPlatform{fakePlatform{id: "opencode"}})
	conn = NewRemoteConn(startRealRemote(t, "tok", "rid", reg), "tok")
	if err := conn.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	rp = newRemotePlatform(conn, "opencode", func() string { return "Box" })
	got, err := rp.RefreshPermissions(ctx, "s1")
	if err != nil || len(got) != 1 || got[0]["id"] != "live" {
		t.Fatalf("RefreshPermissions = %v, %v", got, err)
	}
}
