package remote

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ocman/ocman/internal/platforms"
	pb "github.com/ocman/ocman/internal/remote/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type runningCountResponse struct {
	Count int `json:"count"`
}

func (s *Server) RunningSessionCount(ctx context.Context, req *pb.CallReq) (*pb.JsonResp, error) {
	p, err := s.platformFor(req.Platform)
	if err != nil {
		return nil, err
	}
	var q platforms.SidebarQuery
	if err := json.Unmarshal(req.Payload, &q); err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid running count query")
	}
	counter, ok := p.(platforms.RunningSessionCounter)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "running aggregate unavailable")
	}
	count, err := counter.RunningSessionCount(ctx, q)
	return jsonResp(runningCountResponse{count}, err)
}

func (p *remotePlatform) RunningSessionCount(ctx context.Context, q platforms.SidebarQuery) (int, error) {
	client := p.conn.Client()
	if client == nil {
		return 0, platforms.ErrRemoteOffline
	}
	q.Limit, q.Offset = 0, 0
	raw, err := json.Marshal(q)
	if err != nil {
		return 0, err
	}
	response, err := client.Call(ctx, &pb.CallReq{Method: "RunningSessionCount", Platform: p.base, Payload: raw})
	if err != nil {
		if status.Code(err) != codes.Unimplemented {
			p.conn.markOffline()
		}
		return 0, err
	}
	var result struct {
		Count *int `json:"count"`
	}
	// Older owners reject this distinct method without enumerating any rows.
	// Reject malformed responses too; never fall back to loading session lists.
	if err := unmarshalJSON(response.Payload, &result); err != nil {
		return 0, err
	}
	if result.Count == nil || *result.Count < 0 {
		return 0, fmt.Errorf("invalid running session count")
	}
	p.conn.markSeen()
	return *result.Count, nil
}
