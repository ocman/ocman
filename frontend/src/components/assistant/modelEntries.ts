import type { SessionModelEntry } from '../../lib/api';
import type { SearchKey } from '../../lib/weightedSearch';
import { qualifyModelRef } from '../../lib/modelRef';

// One model row shared by the ModelPicker modal and the ModelSelect field.
// Derived from either rich server-side entries or plain `provider/model`
// strings.
export interface ModelEntry {
  value: string;             // "[platform:]provider/model" — what we send to onSelect
  platform: string;          // base platform, '' when the list has one
  platformName: string;
  provider: string;
  providerName: string;      // human-readable, falls back to provider id
  model: string;
  modelName: string;         // human-readable, falls back to model id
  recentRank: number;        // 1-based recency position, 0 if not recent
  isSessionDefault: boolean;
  isProviderDefault: boolean;
  isAvailable: boolean;
  isFavorite: boolean;
  isCurrent: boolean;
  cooldownUntil: string;     // ISO expiry of the provider's cooldown, '' if none
  description?: string;      // usage hint, when the platform reports one
}

// The server already sorts rich entries (session default → recents →
// provider defaults → available), so we just tag `isCurrent`.
function fromRich(modelEntries: SessionModelEntry[], currentModel: string | undefined): ModelEntry[] {
  return modelEntries.filter((m) => !m.hidden).map((m) => {
    const value = qualifyModelRef(m.platform, m.provider ? `${m.provider}/${m.model}` : m.model);
    return {
      value,
      platform: m.platform || '',
      platformName: m.platformName || m.platform || '',
      provider: m.provider || '',
      providerName: m.providerName || m.provider || '',
      model: m.model,
      modelName: m.modelName || m.model,
      recentRank: m.recentRank ?? 0,
      isSessionDefault: !!m.isSessionDefault,
      isProviderDefault: !!m.isProviderDefault,
      isAvailable: m.isAvailable !== false,
      isFavorite: !!m.isFavorite,
      isCurrent: !!currentModel && value === currentModel,
      cooldownUntil: m.cooldownUntil || '',
      description: m.description || '',
    };
  });
}

// Plain strings carry no ranking data, so they render alphabetically by provider.
export function fromStrings(models: string[], currentModel: string | undefined): ModelEntry[] {
  const entries: ModelEntry[] = models.map((m) => {
    const idx = m.indexOf('/');
    const provider = idx > 0 ? m.slice(0, idx) : '';
    const model = idx > 0 ? m.slice(idx + 1) : m;
    return {
      value: m,
      platform: '',
      platformName: '',
      provider,
      providerName: provider,
      model,
      modelName: model || m,
      recentRank: 0,
      isSessionDefault: false,
      isProviderDefault: false,
      isAvailable: true,
      isFavorite: false,
      isCurrent: !!currentModel && m === currentModel,
      cooldownUntil: '',
    };
  });
  entries.sort((a, b) => {
    if (a.provider !== b.provider) return a.provider.localeCompare(b.provider);
    return a.model.localeCompare(b.model);
  });
  return entries;
}

/** Rich entries take precedence; `rich` unlocks sections and badges. */
export function buildModelEntries(models: string[], modelEntries: SessionModelEntry[] | undefined, currentModel: string | undefined) {
  const rich = !!(modelEntries && modelEntries.length > 0);
  const entries = rich ? fromRich(modelEntries!, currentModel) : fromStrings(models, currentModel);
  // The platform badge only tells rows apart when the list spans platforms.
  const multiPlatform = new Set(entries.map((e) => e.platform || 'opencode')).size > 1;
  return { rich, multiPlatform, entries };
}

// A favorited model always wins the Favorites bucket even when it's also a
// recent/default — the star is the stronger opt-in.
export function modelSectionOf(e: ModelEntry): string {
  if (e.isFavorite) return 'Favorites';
  if (e.isSessionDefault || e.recentRank > 0) return 'Recent';
  if (e.isProviderDefault) return 'Recommended';
  if (e.isAvailable) return 'All models';
  return 'Disconnected providers';
}

// Favorites and defaults outrank closer matches when searching.
export const isPinnedModel = (e: ModelEntry) => e.isFavorite || e.isSessionDefault || e.isProviderDefault;

// Model display name + id get the most weight (what users type first);
// provider name/id rank below; the full value is lowest so a partial
// match doesn't inflate it.
export const modelSearchKeys: SearchKey<ModelEntry>[] = [
  { name: 'modelName', weight: 1.0 },
  { name: 'model', weight: 0.9 },
  { name: 'providerName', weight: 0.5 },
  { name: 'provider', weight: 0.5 },
  { name: 'platformName', weight: 0.4 },
  { name: 'value', weight: 0.2 },
];
