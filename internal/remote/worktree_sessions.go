package remote

import (
	"context"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	pb "github.com/ocman/ocman/internal/remote/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Retain nativeChild for compatibility with older maintenance inventory peers.
type worktreeSession struct {
	db.Session
	NativeChild bool `json:"nativeChild"`
}

func (p *remotePlatform) WorktreeSessions(ctx context.Context) ([]db.Session, error) {
	rows, err := jsonCall(ctx, p, "WorktreeSessions", "", nil, &[]worktreeSession{})
	if err != nil {
		return nil, err
	}
	out := make([]db.Session, 0, len(*rows))
	for _, row := range *rows {
		row.SourceChild = row.NativeChild
		out = append(out, row.Session)
	}
	return out, nil
}

func (s *Server) WorktreeSessions(ctx context.Context, req *pb.CallReq) (*pb.JsonResp, error) {
	p, err := s.platformFor(req.Platform)
	if err != nil {
		return nil, err
	}
	reader, ok := p.(platforms.WorktreeSessionReader)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "owner lacks complete worktree inventory")
	}
	rows, err := reader.WorktreeSessions(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]worktreeSession, 0, len(rows))
	for _, row := range rows {
		out = append(out, worktreeSession{Session: row, NativeChild: row.SourceChild})
	}
	return jsonResp(out, nil)
}
