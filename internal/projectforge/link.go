package projectforge

import (
	"context"
	"github.com/ocman/ocman/internal/forge"
)

func (m *Module) linkPR(ctx context.Context, req handleRequest, platform, id, dir string, pr *forge.PR) {
	if req.Type == "pr" && m.LinkPR != nil {
		m.LinkPR(ctx, platform, id, dir, req.RemoteID, req.Remote, req.Number, pr)
	}
}
