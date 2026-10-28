package claudecode

import (
	"context"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/provider"
)

// externalAdapter launches on the transcript's owner-local directory, never in
// the browsing supervisor. The provider records adoption on the first send.
func (p *claudePlatform) externalAdapter(ctx context.Context, id string) (*provider.Adapter, error) {
	detail, err := p.RuntimeDetail(ctx, id, 1, 0)
	if err != nil {
		return nil, err
	}
	if detail.Session == nil || !detail.Session.LiveConnection {
		return nil, platforms.ErrUnsupported
	}
	_, a, err := p.projects.ensure(ctx, detail.Session.Directory)
	if err != nil {
		return nil, err
	}
	// Seed session routing before waking browser streams. attach's earlier
	// notification can race the first browser reconnect, before adoption.
	if _, err := a.RuntimeDetail(ctx, id, 1, 0); err != nil {
		return nil, err
	}
	p.projects.bindSession(id, a)
	return a, nil
}
