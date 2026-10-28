package sessionlifecycle

import (
	"context"
	"net/http"

	"github.com/ocman/ocman/internal/httpx"
	"github.com/ocman/ocman/internal/platforms"
)

// HandlePrepareSession reads catalogs without launching an instance.
func (m *Module) HandlePrepareSession(w http.ResponseWriter, r *http.Request) {
	var req Target
	if !httpx.ReadJSON(w, r, httpx.MaxRequestBody, &req) || !req.Validate(w) {
		return
	}
	if !req.ResolvePlatform(r.Context(), m, w, "") {
		return
	}
	if _, ok := m.draftOwner(w, req.Platform, req.Directory); !ok {
		return
	}
	sessions := m.d.Sessions()
	catalog, platformID, err := sessions.DirectoryCatalog(r.Context(), req.Platform, platforms.DirectoryCatalogRequest{Directory: req.Directory})
	if err != nil {
		writeSessionError(w, "preparing session", err)
		return
	}
	if catalog == nil {
		catalog = &platforms.DirectoryCatalog{}
	}
	if catalog.Agents == nil {
		catalog.Agents = []platforms.AgentCatalogEntry{}
	}
	if catalog.Commands == nil {
		catalog.Commands = []platforms.SlashCommandEntry{}
	}
	if catalog.Models == nil {
		catalog.Models = &platforms.SessionModelsResponse{Models: []platforms.SessionModel{}}
	}
	catalog.Models.Models = m.MachineModels(r.Context(), platformID, req.Directory, catalog.Models.Models)
	if sessions != nil {
		for i := range catalog.Models.Models {
			if t := sessions.CooldownUntil(catalog.Models.Models[i].Provider); !t.IsZero() {
				catalog.Models.Models[i].CooldownUntil = &t
			}
		}
	}
	httpx.WriteJSON(w, map[string]any{
		"platform":            platformID,
		"agents":              catalog.Agents,
		"commands":            catalog.Commands,
		"models":              catalog.Models,
		"defaultAgent":        catalog.DefaultAgent,
		"defaultModel":        platforms.QualifyModel(platforms.ID(platformID).Base(), catalog.DefaultModel),
		"projectDefaultModel": m.d.ProjectDefaultModel(r.Context(), req.Directory),
		"liveConnection":      catalog.LiveConnection,
	})
}

// MachineModels tags models from composer-capable platforms on the selected machine.
// own is the selected platform's catalog, which has already been loaded.
func (m *Module) MachineModels(ctx context.Context, selected, directory string, own []platforms.SessionModel) []platforms.SessionModel {
	tag := func(id platforms.ID, models []platforms.SessionModel) []platforms.SessionModel {
		base := id.Base()
		for i := range models {
			models[i].Platform, models[i].PlatformName = string(base), platforms.DisplayName(base)
		}
		return models
	}
	out := tag(platforms.ID(selected), own)
	if m.d.Platforms == nil {
		return out
	}
	for _, p := range m.d.Platforms() {
		if p.ID() == platforms.ID(selected) || remoteIDForPlatform(string(p.ID())) != remoteIDForPlatform(selected) ||
			!p.Capabilities().Composer || !p.Available(ctx) {
			continue
		}
		catalog, err := p.DirectoryCatalog(ctx, platforms.DirectoryCatalogRequest{Directory: directory})
		if err != nil || catalog == nil || catalog.Models == nil {
			continue // another platform's catalog is optional
		}
		out = append(out, tag(p.ID(), catalog.Models.Models)...)
	}
	return out
}
