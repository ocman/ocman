package codexprovider

import (
	"context"

	"github.com/ocman/ocman/internal/platforms"
	codexappserver "github.com/ocman/ocman/internal/platforms/codex/appserver"
)

// ModelProvider is the provider half of Codex model references,
// "codex:openai/<model>"; sends use the model alone.
const ModelProvider = "openai"

// Catalog lists the models model/list reports. Hidden models stay out of
// the picker; reasoning efforts become the composer's variants.
func Catalog(models []codexappserver.Model) *platforms.DirectoryCatalog {
	out := &platforms.SessionModelsResponse{HasProviders: true, Models: []platforms.SessionModel{}}
	for _, m := range models {
		if m.Hidden {
			continue
		}
		var efforts []string
		for _, e := range m.SupportedReasoningEfforts {
			efforts = append(efforts, e.ReasoningEffort)
		}
		name := m.DisplayName
		if name == "" {
			name = m.Model
		}
		out.Models = append(out.Models, platforms.SessionModel{Provider: ModelProvider, ProviderName: "OpenAI", Model: m.Model, ModelName: name,
			Description: m.Description, Reasoning: efforts, IsAvailable: true, IsProviderDefault: m.IsDefault})
		if m.IsDefault {
			out.ProviderDefaults = map[string]string{ModelProvider: m.Model}
		}
	}
	return &platforms.DirectoryCatalog{Models: out, Agents: []platforms.AgentCatalogEntry{}, Commands: []platforms.SlashCommandEntry{}, LiveConnection: true}
}

// catalog serves GET /catalog from the supervisor's app-server: models and
// the directory's commands.
func (b *backend) catalog(ctx context.Context, dir string) (*platforms.DirectoryCatalog, error) {
	c, _, err := b.conn(ctx)
	if err != nil {
		return nil, err
	}
	models, err := c.ListModels(ctx, false)
	if err != nil {
		return nil, err
	}
	out := Catalog(models)
	if dir != "" {
		skills, err := c.ListSkills(ctx, dir)
		if err != nil {
			return nil, err
		}
		out.Commands = Commands(skills)
	}
	return out, nil
}
