package opencode

import (
	"context"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/srvtiming"
	"github.com/ocman/ocman/internal/state"
	log "github.com/sirupsen/logrus"
)

func (a *Adapter) DirectoryCatalogFromStored(ctx context.Context, req platforms.DirectoryCatalogRequest, recents []db.RecentModel, defaults db.SessionDefaults, live bool) (*platforms.DirectoryCatalog, error) {
	port := ""
	if live {
		port = req.Port
		if port == "" {
			port = a.rt.discoverOpenCodePortCtx(ctx, req.Directory)
		}
	}
	models, err := a.modelsFromHistory(ctx, req.Directory, port, recents, defaults)
	if err != nil {
		return nil, err
	}
	out := &platforms.DirectoryCatalog{Agents: []platforms.AgentCatalogEntry{}, Commands: []platforms.SlashCommandEntry{}, Models: models, DefaultAgent: defaults.Agent, DefaultModel: defaults.Model, LiveConnection: port != ""}
	if port != "" {
		out.Agents, err = a.readAgentCatalog(ctx, port, "", req.Directory)
		if err != nil {
			return nil, err
		}
		out.Commands, err = a.readSlashCommands(ctx, port, "", req.Directory)
		if err != nil {
			return nil, err
		}
	}
	return out, ctx.Err()
}

func (a *Adapter) modelsFromHistory(ctx context.Context, directory, port string, recents []db.RecentModel, defaults db.SessionDefaults) (*platforms.SessionModelsResponse, error) {
	var providers OpenCodeProvidersResponse
	hasProviders := port != ""
	if port != "" {
		phase := srvtiming.Begin(ctx, "http_provider")
		var err error
		providers, err = a.readProviderCatalog(ctx, port, directory)
		phase.EndWithDesc("GET /provider")
		if err != nil {
			return nil, err
		}
	}
	var favorites []state.ModelFavorite
	if a.favorites != nil {
		phase := srvtiming.Begin(ctx, "db_favorites")
		var err error
		favorites, err = a.favorites.ModelFavorites(ctx, string(PlatformID))
		phase.End()
		if err != nil {
			log.WithError(err).Warn("opencode: fetching model favorites")
		}
	}
	entries := buildSessionModelEntries(recents, favorites, providers, hasProviders, defaults.Model)
	var connectedDefaults map[string]string
	if hasProviders && len(providers.Default) > 0 {
		connectedDefaults = map[string]string{}
		for _, id := range providers.Connected {
			if model, ok := providers.Default[id]; ok {
				connectedDefaults[id] = model
			}
		}
	}
	return &platforms.SessionModelsResponse{SessionDefault: defaults.Model, ProviderDefaults: connectedDefaults, HasProviders: hasProviders, Models: entries}, nil
}
