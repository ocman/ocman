package owned

import (
	"context"
	"errors"
	"strings"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/statev2"
)

func (a *Adapter) storedCatalogHistory(ctx context.Context, directory, exclude string) ([]db.RecentModel, db.SessionDefaults, error) {
	source := a.source.SourceKey
	if exclude != "" {
		identity, err := a.storedIdentity(ctx, exclude)
		if err != nil {
			return nil, db.SessionDefaults{}, err
		}
		source = identity.SourceKey
	}
	var instances []statev2.Instance
	var err error
	if source == "" {
		instances, err = a.history.Instances(ctx, a.owner, a.source.Platform)
	} else {
		var instance statev2.Instance
		instance, err = a.history.FindInstance(ctx, a.owner, a.source.Platform, source)
		if errors.Is(err, statev2.ErrNotFound) {
			return []db.RecentModel{}, db.SessionDefaults{}, nil
		}
		instances = []statev2.Instance{instance}
	}
	if err != nil {
		return nil, db.SessionDefaults{}, err
	}
	if len(instances) == 0 {
		return []db.RecentModel{}, db.SessionDefaults{}, nil
	}
	ids := make([]string, 0, len(instances))
	for _, instance := range instances {
		ids = append(ids, instance.ID)
	}
	base, err := db.OpenHistoryAnalytics(a.history.FilePath(), ids)
	if err != nil {
		return nil, db.SessionDefaults{}, err
	}
	defer base.Close()
	return base.CatalogPreferences(ctx, exclude, directory)
}

func (a *Adapter) storedDirectoryCatalog(ctx context.Context, req platforms.DirectoryCatalogRequest, exclude string) (*platforms.DirectoryCatalog, error) {
	live := a.checkSource(ctx) == nil
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if exclude != "" {
		identity, err := a.storedIdentity(ctx, exclude)
		if err != nil {
			return nil, err
		}
		live = live && identity.SourceKey == a.source.SourceKey
	}
	recents, defaults, err := a.storedCatalogHistory(ctx, req.Directory, exclude)
	if err != nil {
		return nil, err
	}
	if source, ok := a.native.(platforms.StoredCatalogReader); ok {
		return source.DirectoryCatalogFromStored(ctx, req, recents, defaults, live)
	}
	source, hasLiveCatalog := a.native.(platforms.LiveDirectoryCatalogReader)
	if !live || !hasLiveCatalog {
		return historicalCatalog(recents, defaults), nil
	}
	// Generic provider catalogues contain only running CLI configuration. Their
	// remembered session/default lookup is deliberately bypassed.
	catalog, err := source.LiveDirectoryCatalog(ctx, req)
	if err != nil || catalog == nil {
		return catalog, err
	}
	out := *catalog
	if defaults.Agent != "" {
		out.DefaultAgent = defaults.Agent
	}
	if defaults.Model != "" {
		out.DefaultModel = defaults.Model
		if out.Models != nil {
			models := *out.Models
			models.SessionDefault = defaults.Model
			out.Models = &models
		}
	}
	return &out, nil
}

func historicalCatalog(recents []db.RecentModel, defaults db.SessionDefaults) *platforms.DirectoryCatalog {
	models := &platforms.SessionModelsResponse{SessionDefault: defaults.Model, Models: []platforms.SessionModel{}}
	seen := map[string]bool{}
	for i, row := range recents {
		ref := row.Provider + "/" + row.Model
		seen[ref] = true
		models.Models = append(models.Models, platforms.SessionModel{Provider: row.Provider, Model: row.Model, RecentRank: i + 1, IsSessionDefault: ref == defaults.Model})
	}
	if defaults.Model != "" && !seen[defaults.Model] {
		provider, model, hasProvider := strings.Cut(defaults.Model, "/")
		if !hasProvider {
			provider, model = "", defaults.Model
		}
		models.Models = append(models.Models, platforms.SessionModel{Provider: provider, Model: model, IsSessionDefault: true})
	}
	return &platforms.DirectoryCatalog{DefaultAgent: defaults.Agent, DefaultModel: defaults.Model, Models: models, Agents: []platforms.AgentCatalogEntry{}, Commands: []platforms.SlashCommandEntry{}}
}

func (a *Adapter) storedSessionCatalog(ctx context.Context, id string) (*platforms.DirectoryCatalog, error) {
	row, err := a.storedMetadata(ctx, id)
	if err != nil {
		return nil, err
	}
	return a.storedDirectoryCatalog(ctx, platforms.DirectoryCatalogRequest{Directory: row.Directory}, id)
}
