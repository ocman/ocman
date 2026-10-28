package owned

import (
	"context"

	"github.com/ocman/ocman/internal/platforms"
)

var _ platforms.Platform = (*Adapter)(nil)

func (a *Adapter) ID() platforms.ID    { return a.native.ID() }
func (a *Adapter) DisplayName() string { return a.native.DisplayName() }

// Available reports a usable integration or retained history, so a fresh
// source is usable before its first ingestion and a disconnected one stays
// browsable.
func (a *Adapter) Available(ctx context.Context) bool {
	if a.unavailable == nil && a.native.Available(ctx) {
		return true
	}
	instances, err := a.storedReadInstances(ctx, "")
	return err == nil && len(instances) > 0
}

// Capabilities hides panels the integration cannot project from stored
// history; there is no native fallback to serve them.
func (a *Adapter) Capabilities() platforms.Capabilities {
	caps := a.native.Capabilities()
	caps.BackgroundTaskLifecycle = caps.BackgroundTaskStop
	if _, ok := a.native.(platforms.StoredDetailProjector); !ok {
		caps.FileChanges, caps.SessionInfo = false, false
	}
	return caps
}

func (a *Adapter) DirectoryCatalog(ctx context.Context, req platforms.DirectoryCatalogRequest) (*platforms.DirectoryCatalog, error) {
	return a.storedDirectoryCatalog(ctx, req, "")
}

func (a *Adapter) AgentCatalog(ctx context.Context, id string) ([]platforms.AgentCatalogEntry, error) {
	catalog, err := a.storedSessionCatalog(ctx, id)
	if err != nil || catalog == nil {
		return nil, err
	}
	return catalog.Agents, nil
}

func (a *Adapter) SlashCommands(ctx context.Context, id string) ([]platforms.SlashCommandEntry, error) {
	catalog, err := a.storedSessionCatalog(ctx, id)
	if err != nil || catalog == nil {
		return nil, err
	}
	return catalog.Commands, nil
}

func (a *Adapter) SessionModels(ctx context.Context, id string) (*platforms.SessionModelsResponse, error) {
	catalog, err := a.storedSessionCatalog(ctx, id)
	if err != nil || catalog == nil {
		return nil, err
	}
	return catalog.Models, nil
}
