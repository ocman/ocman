import { useEffect, useMemo, useReducer } from 'react';
import type { SessionModelEntry } from '../../lib/api';
import { ModelLabel } from '../ModelLogo';
import { CommandListPicker } from './CommandListPicker';
import { buildModelEntries, isPinnedModel, modelSearchKeys, modelSectionOf, type ModelEntry } from './modelEntries';
import { ModelBadges } from './ModelBadges';
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
  const { rich: useRich, entries } = useMemo(
    () => buildModelEntries(models, modelEntries, currentModel),
    [modelEntries, models, currentModel],
  );

  // Re-render every second while a cooldown is running so the countdown
  // moves and the badge drops the moment it expires.
  const [, tick] = useReducer((n: number) => n + 1, 0);
  const cooldownEnd = useMemo(
    () => Math.max(0, ...entries.map((e) => (e.cooldownUntil ? Date.parse(e.cooldownUntil) || 0 : 0))),
    [entries],
  );
  useEffect(() => {
    if (!open || cooldownEnd <= Date.now()) return;
    const id = setInterval(() => {
      tick();
      if (Date.now() >= cooldownEnd) clearInterval(id);
    }, 1000);
    return () => clearInterval(id);
  }, [open, cooldownEnd]);

  // Render one model row. The check column + click/hover handling live in
  // CommandListPicker; this supplies the model-specific content + favorites.
  const renderRow = (e: ModelEntry) => (
    <>
      <div className="oc-cmd-item-content">
        <span className="oc-cmd-title">
          <ModelLabel model={e.value}>{e.modelName}</ModelLabel>
          <ModelBadges entry={e} rich={useRich} />
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
    <CommandListPicker<ModelEntry>
      open={open}
      entries={entries}
      fuseKeys={modelSearchKeys}
      pinned={isPinnedModel}
      // Section only when we have rich data (string fallback has no section
      // metadata, so a flat alphabetical list is the right default).
      sectionOf={useRich ? modelSectionOf : undefined}
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
