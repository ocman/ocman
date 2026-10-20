package remote

import (
	"context"
	"fmt"

	"github.com/NoUseFreak/ocman/internal/platforms"
	pb "github.com/NoUseFreak/ocman/internal/remote/proto"
)

func (p *remotePlatform) SessionOwnerID() string { return p.conn.RemoteID() }

func (p *remotePlatform) SessionIdentity(ctx context.Context, req platforms.SessionIdentityRequest) (*platforms.SessionIdentity, error) {
	client := p.conn.Client()
	if client == nil {
		return nil, platforms.ErrRemoteOffline
	}
	encoded, err := marshalJSON(req)
	if err != nil {
		return nil, err
	}
	resp, err := client.SessionIdentity(ctx, &pb.PlatformJsonReq{Platform: p.base, Payload: encoded})
	if err != nil {
		return nil, identityClientError(err)
	}
	var info platforms.SessionIdentity
	if err := unmarshalJSON(resp.Payload, &info); err != nil {
		return nil, err
	}
	if info.OwnerID != p.conn.RemoteID() || info.Platform != p.base ||
		(req.ID != "" && info.ID != req.ID) || (req.NativeID != "" && info.NativeID != req.NativeID) {
		return nil, fmt.Errorf("%w: remote session identity mismatch", platforms.ErrPlatformUnreachable)
	}
	return &info, nil
}

func (s *Server) SessionIdentity(ctx context.Context, req *pb.PlatformJsonReq) (*pb.JsonResp, error) {
	var lookup platforms.SessionIdentityRequest
	if err := unmarshalJSON(req.Payload, &lookup); err != nil {
		return nil, err
	}
	info, err := s.sessions.LookupSessionIdentity(ctx, req.Platform, lookup)
	return jsonResp(info, identityRPCError(err))
}

func (p *remotePlatform) SessionIdentities(ctx context.Context, ids []string) ([]platforms.SessionIdentity, error) {
	client := p.conn.Client()
	if client == nil {
		return nil, platforms.ErrRemoteOffline
	}
	encoded, err := marshalJSON(ids)
	if err != nil {
		return nil, err
	}
	resp, err := client.SessionIdentities(ctx, &pb.PlatformJsonReq{Platform: p.base, Payload: encoded})
	if err != nil {
		return nil, identityClientError(err)
	}
	var rows []platforms.SessionIdentity
	if err := unmarshalJSON(resp.Payload, &rows); err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row.OwnerID != p.conn.RemoteID() || row.Platform != p.base {
			return nil, platforms.ErrPlatformUnreachable
		}
	}
	return rows, nil
}

func (s *Server) SessionIdentities(ctx context.Context, req *pb.PlatformJsonReq) (*pb.JsonResp, error) {
	var ids []string
	if err := unmarshalJSON(req.Payload, &ids); err != nil {
		return nil, err
	}
	rows, err := s.sessions.LookupSessionIdentities(ctx, req.Platform, ids)
	return jsonResp(rows, identityRPCError(err))
}
