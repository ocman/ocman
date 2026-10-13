import type { PrepareSessionResponse } from '../../lib/api';
import { availabilityUnknown } from '../../lib/modelCatalogCache';
import type { CachedProjectSettings, ProjectDefaults } from '../../lib/projectSettingsCache';
import { formatModelRef } from '../../lib/sessionStatus';

export type DraftCatalog = PrepareSessionResponse & { defaults?: ProjectDefaults };

export function draftDefaultModel(catalog: DraftCatalog | undefined, remembered: string | undefined): string {
  const models = catalog?.models.models.filter((entry) => entry.isAvailable !== false).map((entry) => formatModelRef(entry.provider, entry.model)) ?? [];
  return catalog?.projectDefaultModel || (remembered && models.includes(remembered) ? remembered : '') || catalog?.defaultModel || '';
}

export function prepareDraftCatalog(result: PrepareSessionResponse, settings: CachedProjectSettings): DraftCatalog {
  // Catalogs can contain aliases rather than every supported custom model.
  // Only implicit defaults need this provider check; explicit picks stay intact.
  const compatibleModel = (model: string | undefined) => model && result.models.models.some((entry) =>
    model.startsWith(`${entry.provider}/`) && entry.isAvailable !== false) ? model : '';
  const compatibleAgent = (name: string | undefined) => name && result.agents.some((agent) =>
    agent.name === name && agent.mode !== 'subagent') ? name : '';
  return {
    ...result,
    defaultAgent: compatibleAgent(settings.defaultAgent) || result.defaultAgent || '',
    defaults: settings.defaults,
    projectDefaultModel: compatibleModel(settings.defaults?.model) || compatibleModel(result.projectDefaultModel),
    models: { ...result.models, models: result.models.hasProviders ? result.models.models : availabilityUnknown(result.models.models) },
  };
}
