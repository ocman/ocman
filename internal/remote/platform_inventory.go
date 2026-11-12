package remote

import (
	"context"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	pb "github.com/ocman/ocman/internal/remote/proto"
)

var _ platforms.SessionInventoryReader = (*remotePlatform)(nil)

// SessionInventory uses the existing inventory RPC without Sessions' display-only
// stale fallback. An empty authoritative reply is distinct from a failed read.
func (p *remotePlatform) SessionInventory(ctx context.Context, directory string) ([]db.Session, error) {
	client := p.conn.Client()
	if client == nil {
		return nil, platforms.ErrRemoteOffline
	}
	response, err := client.Sessions(ctx, &pb.SessionsReq{Platform: p.base, Dir: directory})
	if err != nil {
		p.conn.markOffline()
		return nil, err
	}
	var rows []db.Session
	if err := unmarshalJSON(response.Payload, &rows); err != nil {
		return nil, err
	}
	p.conn.markSeen()
	return p.checkedSessions(rows, directory, 0)
}
