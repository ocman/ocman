package remote

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/owned"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
	"github.com/NoUseFreak/ocman/internal/state"
)

func (p *remotePlatform) withPluginReferences(core *state.DB) *remotePlatform {
	if core == nil {
		return p
	}
	references, err := owned.NewReferences(core)
	if err == nil {
		p.pluginReference = func(ctx context.Context, id string) (string, error) {
			return references.RemotePluginReference(ctx, string(p.ID()), id)
		}
	}
	return p
}

func (p *remotePlatform) PluginSessionReference(ctx context.Context, id string) (string, error) {
	if !p.CanonicalSessionIDs() {
		return id, nil
	}
	if !sessioncatalog.IsID(id) {
		return "", sessioncatalog.ErrInvalid
	}
	if p.pluginReference == nil {
		return "", platforms.ErrHistoryUnavailable
	}
	return p.pluginReference(ctx, id)
}
