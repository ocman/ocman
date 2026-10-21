package opencode

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/srvtiming"
	"github.com/NoUseFreak/ocman/internal/state"
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
	models, _ := a.modelsFromHistory(ctx, req.Directory, port, recents, defaults)
	out := &platforms.DirectoryCatalog{Agents: []platforms.AgentCatalogEntry{}, Commands: []platforms.SlashCommandEntry{}, Models: models, DefaultAgent: defaults.Agent, DefaultModel: defaults.Model, LiveConnection: port != ""}
	if port != "" {
		if agents := agentCatalogAt(ctx, port, "", req.Directory); agents != nil {
			out.Agents = agents
		}
		if commands := slashCommandsAt(ctx, port, "", req.Directory); commands != nil {
			out.Commands = commands
		}
	}
	return out, ctx.Err()
}

func (a *Adapter) modelsFromHistory(ctx context.Context, directory, port string, recents []db.RecentModel, defaults db.SessionDefaults) (*platforms.SessionModelsResponse, db.SessionDefaults) {
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
	var providers OpenCodeProvidersResponse
	hasProviders := false
	if port != "" {
		phase := srvtiming.Begin(ctx, "http_provider")
		providers, hasProviders = fetchOpenCodeProviders(ctx, port, directory)
		phase.EndWithDesc("GET /provider")
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
	return &platforms.SessionModelsResponse{SessionDefault: defaults.Model, ProviderDefaults: connectedDefaults, HasProviders: hasProviders, Models: entries}, defaults
}
