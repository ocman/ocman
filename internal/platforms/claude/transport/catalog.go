package claudecode

import (
	"context"

	"github.com/ocman/ocman/internal/platforms"
)

// LiveDirectoryCatalog prefers an already attached execution supervisor for
// this directory, so its session/init announcements supplement the CLI probe.
// Drafts and directories with no live execution use the browsing supervisor;
// neither path launches or resumes a session.
func (p *claudePlatform) LiveDirectoryCatalog(ctx context.Context, req platforms.DirectoryCatalogRequest) (*platforms.DirectoryCatalog, error) {
	if p.projects != nil {
		for _, adapter := range p.projects.adapters() {
			rows, err := adapter.RuntimeSessions(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				continue // dead execution does not prevent a configuration probe
			}
			for _, row := range rows {
				// Shared ownership/transcript stores can make another project's
				// runtime inventory list this directory, including on older peers.
				if row.Directory == req.Directory && p.projects.owner(row.ID) == adapter {
					return adapter.LiveDirectoryCatalog(ctx, req)
				}
			}
		}
	}
	return p.Adapter.LiveDirectoryCatalog(ctx, req)
}
