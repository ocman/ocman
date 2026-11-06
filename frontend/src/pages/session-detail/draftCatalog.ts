import type { PrepareSessionResponse, SessionModelEntry } from '../../lib/api';
import { availabilityUnknown } from '../../lib/modelCatalogCache';
import type { CachedProjectSettings, ProjectDefaults } from '../../lib/projectSettingsCache';
import { formatModelRef } from '../../lib/sessionStatus';
import { qualifyModelRef } from '../../lib/modelRef';

export type DraftCatalog = PrepareSessionResponse & { defaults?: ProjectDefaults };

const modelRef = (entry: SessionModelEntry) => qualifyModelRef(entry.platform, formatModelRef(entry.provider, entry.model));
const defaultEligible = (entry: SessionModelEntry) => entry.isAvailable !== false && entry.isDefaultEligible !== false;

function eligibleDefault(catalog: DraftCatalog | undefined, model: string | undefined): string {
  const entry = catalog?.models.models.find((entry) => modelRef(entry) === model);
  return model && (!entry || defaultEligible(entry)) ? model : '';
}

export function draftDefaultModel(catalog: DraftCatalog | undefined, remembered: string | undefined): string {
  const models = catalog?.models.models.filter(defaultEligible) ?? [];
  const providerDefault = models.find((entry) => entry.isProviderDefault);
  return eligibleDefault(catalog, catalog?.projectDefaultModel)
    || (remembered && models.some((entry) => modelRef(entry) === remembered) ? remembered : '')
    || eligibleDefault(catalog, catalog?.defaultModel)
    || (providerDefault ? modelRef(providerDefault) : '');
}

export function prepareDraftCatalog(result: PrepareSessionResponse, settings: CachedProjectSettings): DraftCatalog {
  // Catalogs can contain aliases rather than every supported custom model.
  // Only implicit defaults need this provider check; explicit picks stay intact.
  const compatibleModel = (model: string | undefined) => eligibleDefault(result, model) && model && result.models.models.some((entry) =>
    model.startsWith(qualifyModelRef(entry.platform, `${entry.provider}/`)) && entry.isAvailable !== false) ? model : '';
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
