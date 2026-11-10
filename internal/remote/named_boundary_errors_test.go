package remote

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/google/uuid"
	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	pb "github.com/ocman/ocman/internal/remote/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type boundaryClient struct {
	pb.OcmanClient
	payload []byte
	err     error
}

func (c boundaryClient) Call(context.Context, *pb.CallReq, ...grpc.CallOption) (*pb.JsonResp, error) {
	return &pb.JsonResp{Payload: c.payload}, c.err
}
func (c boundaryClient) Sessions(context.Context, *pb.SessionsReq, ...grpc.CallOption) (*pb.JsonResp, error) {
	return &pb.JsonResp{Payload: c.payload}, c.err
}
func (c boundaryClient) Session(context.Context, *pb.SessionReq, ...grpc.CallOption) (*pb.JsonResp, error) {
	return &pb.JsonResp{Payload: c.payload}, c.err
}
func (c boundaryClient) SessionsInactiveBefore(context.Context, *pb.CutoffReq, ...grpc.CallOption) (*pb.JsonResp, error) {
	return &pb.JsonResp{Payload: c.payload}, c.err
}

func TestNamedRemoteReadsRejectMalformedOwnerPayloadsWithoutAcquiringAuthority(t *testing.T) {
	c := &RemoteConn{client: boundaryClient{payload: []byte(`{`)}, remoteID: "owner", protocolVersion: canonicalSessionProtocol}
	p := newRemotePlatform(c, "cnx_work", nil)
	p.kind = "opencode"
	if _, err := p.Sessions(t.Context(), "", 0); err == nil {
		t.Fatal("malformed sessions accepted")
	}
	if _, err := p.Session(t.Context(), "ocm_"+uuid.NewString(), 10, 0); err == nil {
		t.Fatal("malformed detail accepted")
	}
	if _, err := p.SessionsInactiveBefore(t.Context(), 0); err == nil {
		t.Fatal("malformed archive inventory accepted")
	}
	if _, err := p.loadCapabilities(t.Context()); err == nil {
		t.Fatal("malformed capability grant accepted")
	}
	if _, err := p.AgentCatalog(t.Context(), "id"); err == nil {
		t.Fatal("malformed agents accepted")
	}
	if _, err := p.SessionInfo(t.Context(), "id"); err == nil {
		t.Fatal("malformed session info accepted")
	}
	if _, err := p.SessionIdentity(t.Context(), platforms.SessionIdentityRequest{ID: "id"}); err == nil {
		t.Fatal("malformed identity accepted")
	}
	if _, err := p.SessionIdentities(t.Context(), []string{"id"}); err == nil {
		t.Fatal("malformed identity batch accepted")
	}
	if len(p.owned) != 0 {
		t.Fatal("bad payload acquired ownership")
	}
	c.client = boundaryClient{err: status.Error(codes.Unavailable, "owner disconnected")}
	if _, err := p.ForkSession(t.Context(), platforms.ForkSessionRequest{}); !errors.Is(err, platforms.ErrPlatformUnreachable) {
		t.Fatal(err)
	}
	if _, err := p.CreateSession(t.Context(), platforms.CreateSessionRequest{}); !errors.Is(err, platforms.ErrPlatformUnreachable) {
		t.Fatal(err)
	}
	if rows, err := p.SessionsInactiveBefore(t.Context(), 0); err != nil || rows != nil {
		t.Fatalf("best-effort unavailable archive: %+v %v", rows, err)
	}
	c.client = nil
	if rows, err := p.SessionsInactiveBefore(t.Context(), 0); err != nil || rows != nil {
		t.Fatalf("offline archive: %+v %v", rows, err)
	}
	if err := p.proxySessionEvents(t.Context(), "id", io.Discard, nil); !errors.Is(err, platforms.ErrRemoteOffline) {
		t.Fatal(err)
	}
	if got := (&remotePlatform{base: "codex"}).NativeKind(); got != "codex" {
		t.Fatal(got)
	}
}

func TestNamedRemoteIdentityRejectsWrongOwnerKindAndCanonicalBinding(t *testing.T) {
	id := "ocm_" + uuid.NewString()
	for _, row := range []platforms.SessionIdentity{
		{ID: id, OwnerID: "sibling", Platform: "opencode", SourceKey: "source", CreationState: "ready"},
		{ID: id, OwnerID: "owner", Platform: "cnx_work", SourceKey: "source", CreationState: "ready"},
		{ID: id, OwnerID: "owner", Platform: "opencode", SourceKey: "source", CreationState: "creating"},
	} {
		payload, _ := marshalJSON(row)
		c := &RemoteConn{client: boundaryClient{payload: payload}, remoteID: "owner", protocolVersion: canonicalSessionProtocol}
		p := newRemotePlatform(c, "cnx_work", nil)
		p.kind = "opencode"
		if _, err := p.SessionIdentity(t.Context(), platforms.SessionIdentityRequest{ID: id}); !errors.Is(err, platforms.ErrPlatformUnreachable) {
			t.Fatal(err)
		}
		batch, _ := marshalJSON([]platforms.SessionIdentity{row})
		c.client = boundaryClient{payload: batch}
		if _, err := p.SessionIdentities(t.Context(), []string{id}); !errors.Is(err, platforms.ErrPlatformUnreachable) {
			t.Fatal(err)
		}
	}
	s := NewServer(platforms.NewRegistry(), localStubHost{}, "owner", "test")
	for _, handler := range []func(context.Context, *pb.CallReq) (*pb.JsonResp, error){s.SessionIdentity, s.SessionIdentities} {
		if _, err := handler(t.Context(), &pb.CallReq{Payload: []byte(`{`)}); err == nil {
			t.Fatal("malformed identity lookup accepted")
		}
	}
	if _, err := s.SessionIdentities(t.Context(), &pb.CallReq{Payload: []byte(`"not an array"`)}); err == nil {
		t.Fatal("invalid identity batch accepted")
	}
	if err := s.registry.EnableCanonicalSessions(); err != nil {
		t.Fatal(err)
	}
	for _, handler := range []func(context.Context, *pb.CallReq) (*pb.JsonResp, error){s.SessionIdentity, s.SessionIdentities} {
		if _, err := handler(t.Context(), &pb.CallReq{Platform: "cnx_missing", Payload: []byte(`[]`)}); err == nil {
			t.Fatal("unknown connection lookup accepted")
		}
	}
}

func TestCanonicalNamedResponseValidationCoversNestedReferencesAndUncertainCreation(t *testing.T) {
	p := canonicalRemotePlatform()
	id := "ocm_" + uuid.NewString()
	for _, detail := range []*platforms.SessionDetail{
		{},
		{Session: &db.Session{ID: id}, SessionTree: []db.Session{{ID: "native-child"}}},
		{Session: &db.Session{ID: id}, Messages: []db.Message{{SessionID: "native-session"}}},
	} {
		if _, err := p.checkedDetail(detail, id); !errors.Is(err, platforms.ErrPlatformUnreachable) {
			t.Fatal(err)
		}
	}
	if _, err := canonicalJSONResult(p, &db.Session{ID: id}); err != nil {
		t.Fatal(err)
	}
	if _, err := canonicalJSONResult(p, &platforms.SessionChanges{SessionID: id}); err != nil {
		t.Fatal(err)
	}
	if _, err := canonicalJSONResult(p, &platforms.SessionInfo{SessionID: id}); err != nil {
		t.Fatal(err)
	}
	if _, err := canonicalDecodeResult(p, []byte(`{`), &platforms.SessionInfo{}); err == nil {
		t.Fatal("malformed canonical response accepted")
	}
	_, err := canonicalDecodeResult(p, []byte(`{"id":"`+id+`","replayed":"wrong type"}`), &platforms.CreateSessionResponse{})
	var pending *platforms.CreationPendingError
	if !errors.As(err, &pending) || pending.SessionID != id {
		t.Fatalf("uncertain creation lost owner identity: %v", err)
	}
}
