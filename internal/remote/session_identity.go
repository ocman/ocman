package remote

import (
	"bytes"
	"context"
	"fmt"

	"github.com/ocman/ocman/internal/platforms"
	pb "github.com/ocman/ocman/internal/remote/proto"
)

func (p *remotePlatform) SessionOwnerID() string { return p.conn.RemoteID() }

func (p *remotePlatform) SessionIdentity(ctx context.Context, req platforms.SessionIdentityRequest) (*platforms.SessionIdentity, error) {
	payload, err := p.call(ctx, "SessionIdentity", "", req)
	if err != nil {
		return nil, identityClientError(err)
	}
	var info platforms.SessionIdentity
	if err := unmarshalJSON(payload, &info); err != nil {
		return nil, err
	}
	if info.OwnerID != p.conn.RemoteID() || info.Platform != p.base ||
		(req.ID != "" && info.ID != req.ID) || (!p.CanonicalSessionIDs() && req.NativeID != "" && info.NativeID != req.NativeID) ||
		(p.CanonicalSessionIDs() && !p.canonicalIdentity(info)) {
		return nil, fmt.Errorf("%w: remote session identity mismatch", platforms.ErrPlatformUnreachable)
	}
	return &info, nil
}

func (s *Server) SessionIdentity(ctx context.Context, req *pb.CallReq) (*pb.JsonResp, error) {
	if s.registry.CanonicalSessionIDs() {
		if _, err := s.platformFor(req.Platform); err != nil {
			return nil, err
		}
	}
	var lookup platforms.SessionIdentityRequest
	if err := unmarshalJSON(req.Payload, &lookup); err != nil {
		return nil, err
	}
	info, err := s.sessions.LookupSessionIdentity(ctx, req.Platform, lookup)
	return jsonResp(info, identityRPCError(err))
}

func (p *remotePlatform) SessionIdentities(ctx context.Context, ids []string) ([]platforms.SessionIdentity, error) {
	payload, err := p.call(ctx, "SessionIdentities", "", ids)
	if err != nil {
		return nil, identityClientError(err)
	}
	var rows []platforms.SessionIdentity
	if err := unmarshalJSON(payload, &rows); err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row.OwnerID != p.conn.RemoteID() || row.Platform != p.base || (p.CanonicalSessionIDs() && !p.canonicalIdentity(row)) {
			return nil, platforms.ErrPlatformUnreachable
		}
	}
	return rows, nil
}

func (s *Server) SessionIdentities(ctx context.Context, req *pb.CallReq) (*pb.JsonResp, error) {
	if s.registry.CanonicalSessionIDs() {
		if _, err := s.platformFor(req.Platform); err != nil {
			return nil, err
		}
	}
	if payload := bytes.TrimSpace(req.Payload); len(payload) > 0 && payload[0] == '{' {
		var lookup legacyReferenceRequest
		if err := unmarshalJSON(payload, &lookup); err != nil {
			return nil, err
		}
		rows, err := s.sessions.ResolveLegacyReferences(ctx, req.Platform, lookup.References)
		return jsonResp(rows, identityRPCError(err))
	}
	var ids []string
	if err := unmarshalJSON(req.Payload, &ids); err != nil {
		return nil, err
	}
	rows, err := s.sessions.LookupSessionIdentities(ctx, req.Platform, ids)
	return jsonResp(rows, identityRPCError(err))
}
