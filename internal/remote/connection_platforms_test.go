package remote

import (
	"context"
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platforms"
	pb "github.com/ocman/ocman/internal/remote/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type namedPlatform struct {
	fakePlatform
	name string
}

func (p *namedPlatform) DisplayName() string { return p.name }

func TestManagerPublishesSameKindNamedRemoteConnections(t *testing.T) {
	owner := platforms.NewRegistry()
	for _, name := range []string{"personal", "work"} {
		p := &namedPlatform{fakePlatform: fakePlatform{id: "codex", sessions: []db.Session{{ID: name, Platform: "cnx_" + name}}}, name: name}
		if err := owner.RegisterConnection(platforms.ID("cnx_"+name), p); err != nil {
			t.Fatal(err)
		}
	}
	addr := startRealRemote(t, "tok", "named", owner)
	conn := NewRemoteConn(addr, "tok")
	t.Cleanup(conn.Close)
	if err := conn.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	meta := conn.ConnectionPlatforms()
	if len(meta) != 2 || meta[0].ID != "cnx_personal" || meta[1].ID != "cnx_work" || meta[1].Kind != "codex" || meta[1].DisplayName != "work" {
		t.Fatalf("%+v", meta)
	}
	m := NewManager(platforms.NewRegistry(), hostsvc.NewRouter(localStubHost{}), nil, "codex")
	mr := &managedRemote{localID: 1, conn: conn}
	m.remotes[1] = mr
	m.connectAndRegisterTest(t, mr)
	for _, name := range []string{"personal", "work"} {
		p, ok := m.registry.Get(platforms.ID("r-named:cnx_" + name))
		if !ok {
			t.Fatalf("missing %s", name)
		}
		if p.DisplayName() != name || p.(*remotePlatform).NativeKind() != "codex" {
			t.Fatalf("lost native kind/name: %s", p.DisplayName())
		}
		rows, err := p.Sessions(t.Context(), "", 0)
		if err != nil || len(rows) != 1 || rows[0].ID != name || rows[0].Platform != "r-named:cnx_"+name {
			t.Fatalf("%+v %v", rows, err)
		}
	}
	pinned := mr.platforms[0]
	m.disconnect(1)
	if len(m.registry.Entries()) != 0 {
		t.Fatal("named remote survived disconnect")
	}
	if _, err := pinned.DirectoryCatalog(t.Context(), platforms.DirectoryCatalogRequest{Directory: "/owner"}); !errors.Is(err, platforms.ErrRemoteOffline) {
		t.Fatal(err)
	}
}

type oldMetadataClient struct{ pb.OcmanClient }

func (oldMetadataClient) Call(context.Context, *pb.CallReq, ...grpc.CallOption) (*pb.JsonResp, error) {
	return nil, status.Error(codes.Unimplemented, "old peer")
}

func TestConnectionMetadataOldPeerCompatibility(t *testing.T) {
	got, err := loadConnectionPlatforms(t.Context(), oldMetadataClient{}, []string{"opencode", "codex"})
	if err != nil || len(got) != 2 || got[1].Kind != "codex" {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := loadConnectionPlatforms(t.Context(), oldMetadataClient{}, []string{"cnx_unknown"}); err == nil {
		t.Fatal("named peer without metadata accepted")
	}
}

func TestNamedRemoteCreationUsesExactDefaultWithoutSiblingFallback(t *testing.T) {
	c := &RemoteConn{platforms: []string{"cnx_personal", "cnx_work"}, connectionPlatforms: []ConnectionPlatformMetadata{{ID: "cnx_personal", Kind: "codex", Selectable: true}, {ID: "cnx_work", Kind: "codex", Selectable: true, Default: true}}}
	if got := c.creationPlatform("codex"); got != "cnx_work" {
		t.Fatal(got)
	}
	if got := c.creationPlatform("cnx_personal"); got != "cnx_personal" {
		t.Fatal(got)
	}
	if got := c.creationPlatform("cnx_removed"); got != "" {
		t.Fatal("removed account fell back", got)
	}
	c.connectionPlatforms[1].Selectable = false
	if got := c.creationPlatform("codex"); got != "" {
		t.Fatal("disabled default fell back", got)
	}
	if got := c.creationPlatform("cnx_work"); got != "" {
		t.Fatal("disabled explicit account fell back", got)
	}
}
