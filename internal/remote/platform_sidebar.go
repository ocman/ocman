package remote

import (
	"context"
	"encoding/json"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	pb "github.com/ocman/ocman/internal/remote/proto"
)

func (p *remotePlatform) SidebarSessions(ctx context.Context, q platforms.SidebarQuery) ([]db.Session, error) {
	client := p.conn.Client()
	if client == nil {
		return nil, platforms.ErrRemoteOffline
	}
	raw, err := json.Marshal(q)
	if err != nil {
		return nil, err
	}
	response, err := client.Sessions(ctx, &pb.SessionsReq{Platform: p.base, Dir: q.Directory, Since: q.Since, SidebarQuery: raw})
	if err != nil {
		p.conn.markOffline()
		return nil, err
	}
	var rows []db.Session
	if err := unmarshalJSON(response.Payload, &rows); err != nil {
		return nil, err
	}
	p.conn.markSeen()
	// Owner-side paging already preserves pinned/snoozed sessions outside the
	// time window. The hub validates identities but must not filter them again.
	return p.checkedSessions(rows, q.Directory, 0)
}
