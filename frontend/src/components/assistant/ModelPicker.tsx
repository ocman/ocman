import { useMemo } from 'react';
import type { SessionModelEntry } from '../../lib/api';
import { CommandListPicker, type PickerEntryBase } from './CommandListPicker';
import './ModelPicker.css';

export interface ModelPickerProps {
  open: boolean;
  models: string[];
  // When provided, takes precedence over `models` and unlocks badges,
  // provider names, and favorites-aware sorting. `models` remains the
  // fallback for backward compatibility.
  modelEntries?: SessionModelEntry[];
  currentModel?: string;
  initialQuery?: string;
  onSelect: (model: string) => void;
  // Optional star-toggle handler. When provided, each row renders a
  // clickable star that calls back with the desired next state
  // (true = favorite, false = unfavorite). The parent owns the
  // persistence; the picker just flips the UI via the updated
  // `modelEntries` it receives on re-render.
  onToggleFavorite?: (provider: string, model: string, nextFavorite: boolean) => void;
  onClose: () => void;
  onBack?: () => void;
}

// Internal row type used for rendering. Derived from either `modelEntries`
// (rich server-side merged data) or `models` (plain `provider/model` strings).
interface PickerEntry extends PickerEntryBase {
  value: string;             // "provider/model" — what we send to onSelect
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
}

// buildEntriesFromRich turns the backend response into picker rows. The server
// already sorts (session default → recents → provider defaults → available),
// so we just tag `isCurrent` and return as-is.
function buildEntriesFromRich(
  modelEntries: SessionModelEntry[],
  currentModel: string | undefined,
): PickerEntry[] {
  return modelEntries.map((m) => {
    const value = m.provider ? `${m.provider}/${m.model}` : m.model;
    return {
      value,
      provider: m.provider || '',
      providerName: m.providerName || m.provider || '',
      model: m.model,
      modelName: m.modelName || m.model,
      recentRank: m.recentRank ?? 0,
      isSessionDefault: !!m.isSessionDefault,
      isProviderDefault: !!m.isProviderDefault,
      isAvailable: !!m.isAvailable,
      isFavorite: !!m.isFavorite,
      isCurrent: !!currentModel && value === currentModel,
    };
  });
}

// buildEntriesFromStrings is the fallback when only `provider/model` strings
// are available (old backends, pre-`modelEntries` flow). No favorites data —
// we just render an alphabetical list grouped by provider.
function buildEntriesFromStrings(models: string[], currentModel: string | undefined): PickerEntry[] {
  const entries: PickerEntry[] = models.map((m) => {
    const idx = m.indexOf('/');
    const provider = idx > 0 ? m.slice(0, idx) : '';
    const model = idx > 0 ? m.slice(idx + 1) : m;
    return {
      value: m,
      provider,
      providerName: provider,
      model,
      modelName: model || m,
      recentRank: 0,
      isSessionDefault: false,
      isProviderDefault: false,
      isAvailable: false,
      isFavorite: false,
      isCurrent: !!currentModel && m === currentModel,
    };
  });
  entries.sort((a, b) => {
    if (a.provider !== b.provider) return a.provider.localeCompare(b.provider);
    return a.model.localeCompare(b.model);
  });
  return entries;
}

// sectionOf buckets a model row into a display section. The server already
// orders entries (session default → recents → provider defaults → available),
// so the picker just emits headers as the section changes. A favorited model
// always wins the Favorites bucket even when it's also a recent/default — the
// star is the stronger opt-in and keeping it in one spot avoids ambiguity.
function sectionOf(e: PickerEntry): string {
  if (e.isFavorite) return 'Favorites';
  if (e.isSessionDefault || e.recentRank > 0) return 'Recent';
  if (e.isProviderDefault) return 'Recommended';
  if (e.isAvailable) return 'All models';
  return 'Archived';
}

// Command-palette–style modal for picking a model. Reuses the visual styling
// of `CommandPalette` so it feels consistent with the rest of the app.
export function ModelPicker({
  open,
  models,
  modelEntries,
  currentModel,
  initialQuery,
  onSelect,
  onToggleFavorite,
  onClose,
  onBack,
}: ModelPickerProps) {
  const useRich = !!(modelEntries && modelEntries.length > 0);

  const entries = useMemo<PickerEntry[]>(
    () => useRich ? buildEntriesFromRich(modelEntries!, currentModel) : buildEntriesFromStrings(models, currentModel),
    [useRich, modelEntries, models, currentModel],
  );

  // Render one model row. The check column + click/hover handling live in
  // CommandListPicker; this supplies the model-specific content + favorites.
  const renderRow = (e: PickerEntry) => (
    <>
      <div className="oc-cmd-item-content">
        <span className="oc-cmd-title">
          {e.modelName}
          {e.isSessionDefault && (
            <span className="oc-model-picker-badge oc-model-picker-badge--star" title="Session default">
              <i className="bi bi-star-fill" />
            </span>
          )}
          {!e.isSessionDefault && e.recentRank > 0 && (
            <span className="oc-model-picker-badge oc-model-picker-badge--used" title="Recently used">
              <i className="bi bi-clock-history" />
            </span>
          )}
          {e.isProviderDefault && !e.isSessionDefault && (
            <span className="oc-model-picker-badge oc-model-picker-badge--default" title="Provider default">
              default
            </span>
          )}
          {useRich && !e.isAvailable && (
            <span className="oc-model-picker-badge oc-model-picker-badge--archived" title="Provider not connected">
              archived
            </span>
          )}
        </span>
        <span className="oc-cmd-meta">{e.providerName || e.provider || ''}</span>
      </div>
      {onToggleFavorite && e.provider && (
        <button
          type="button"
          className={`oc-model-picker-fav${e.isFavorite ? ' oc-model-picker-fav--on' : ''}`}
          aria-label={e.isFavorite ? 'Remove from favorites' : 'Add to favorites'}
          title={e.isFavorite ? 'Unfavorite' : 'Favorite'}
          // Stop propagation so clicking the star doesn't also pick the
          // model and close the picker.
          onClick={(ev) => {
            ev.stopPropagation();
            onToggleFavorite(e.provider, e.model, !e.isFavorite);
          }}
        >
          <i className={e.isFavorite ? 'bi bi-star-fill' : 'bi bi-star'} />
        </button>
      )}
    </>
  );

  return (
    <CommandListPicker<PickerEntry>
      open={open}
      entries={entries}
      // Model display name + id get the most weight (what users type first);
      // provider name/id rank below; the full value is lowest so a partial
      // match doesn't inflate it.
      fuseKeys={[
        { name: 'modelName', weight: 1.0 },
        { name: 'model', weight: 0.9 },
        { name: 'providerName', weight: 0.5 },
        { name: 'provider', weight: 0.5 },
        { name: 'value', weight: 0.2 },
      ]}
      // Section only when we have rich data (string fallback has no section
      // metadata, so a flat alphabetical list is the right default).
      sectionOf={useRich ? sectionOf : undefined}
      renderRow={renderRow}
      placeholder={(total) => total > 0 ? `Select a model (${total} available)...` : 'Select a model...'}
      emptyMessage="No models found"
      isCurrent={(e) => e.isCurrent}
      initialQuery={initialQuery}
      onSelect={onSelect}
      onClose={onClose}
      onBack={onBack}
    />
  );
}
