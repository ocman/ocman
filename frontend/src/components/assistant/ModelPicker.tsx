import { useEffect, useReducer, useRef, type KeyboardEvent, type ReactNode } from 'react';
import type { SessionModelEntry } from '../../lib/api';
import { ModelLabel } from '../ModelLogo';
import { PlatformLogo } from '../PlatformBadge';
import { Button } from '../Control';
import { CommandListPicker } from './CommandListPicker';
import { buildModelEntries, isPinnedModel, modelSearchKeys, modelSectionOf, type ModelEntry } from './modelEntries';
import { ModelBadges } from './ModelBadges';
import { useModelPlatformTabs } from './useModelPlatformTabs';
import { describeModel } from './composerModel';
import type { PickerTabsConfig } from '../PickerTabs';
import './ModelPicker.css';

export interface ModelPickerContext {
  tabs?: PickerTabsConfig;
  status?: ReactNode;
}

export interface ModelPickerProps {
  context?: ModelPickerContext;
  open: boolean;
  models: string[];
  // When provided, takes precedence over `models` and unlocks badges,
  // provider names, and favorites-aware sorting. `models` remains the
  // fallback for backward compatibility.
  modelEntries?: SessionModelEntry[];
  currentModel?: string;
  initialQuery?: string;
  onSelect: (model: string) => void;
  reasoning?: { current?: string; onSelect: (value: string) => void };
  // Optional star-toggle handler. When provided, each row renders a
  // clickable star that calls back with the desired next state
  // (true = favorite, false = unfavorite). The parent owns the
  // persistence; the picker just flips the UI via the updated
  // `modelEntries` it receives on re-render.
  onToggleFavorite?: (provider: string, model: string, nextFavorite: boolean, platform?: string) => void;
  onClose: () => void;
  onBack?: () => void;
}

function handleReasoningKey(event: KeyboardEvent<HTMLDivElement>) {
  if (event.defaultPrevented || event.altKey || event.ctrlKey || event.metaKey) return;
  const { currentTarget, target, key } = event;
  if (key !== 'Tab' && key !== 'ArrowLeft') return;
  const levels = Array.from(currentTarget.querySelectorAll<HTMLButtonElement>('.oc-model-reasoning button'));
  if (levels.length === 0) return;
  const focused = levels.findIndex((button) => button === target);
  if (key === 'Tab') {
    const index = focused >= 0 ? focused : levels.findIndex((button) => button.getAttribute('aria-pressed') === 'true');
    levels[(index + (event.shiftKey ? -1 : 1) + levels.length) % levels.length].focus();
  } else if (key === 'ArrowLeft' && focused >= 0) {
    currentTarget.querySelector<HTMLInputElement>('[role="combobox"]')?.focus();
  } else {
    return;
  }
  event.preventDefault();
  event.stopPropagation();
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
  context,
  reasoning,
}: ModelPickerProps) {
  const { rich: useRich, multiPlatform, entries } = buildModelEntries(models, modelEntries, currentModel);
  const { tabs, visibleEntries } = useModelPlatformTabs(entries);
  const currentLevelRef = useRef<HTMLButtonElement>(null);
  const optionsFor = (model: string) => describeModel(model, modelEntries).reasoningOptions;
  const options = optionsFor(currentModel ?? '');
  const level = reasoning?.current ?? '';
  const activeLevel = options.includes(level) ? level : '';
  const sidebar = !context?.status && reasoning && options.length > 0 && (
    <div className="oc-model-reasoning" role="group" aria-label="Reasoning level">
      <div className="oc-model-picker-title">Reasoning level</div>
      {['', ...options].map((value) => (
        <Button key={value} ref={value === activeLevel ? currentLevelRef : undefined}
          type="button" variant="ghost" size="small" aria-pressed={value === activeLevel}
          onClick={() => { reasoning.onSelect(value); onClose(); }}>
          {value || 'Default'}
          {value === activeLevel && <i className="bi bi-check2" aria-hidden="true" />}
        </Button>
      ))}
    </div>
  );

  // Re-render every second while a cooldown is running so the countdown
  // moves and the badge drops the moment it expires.
  const [, tick] = useReducer((n: number) => n + 1, 0);
  const cooldownEnd = Math.max(0, ...entries.map((e) => (e.cooldownUntil ? Date.parse(e.cooldownUntil) || 0 : 0)));
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
        <span className="oc-cmd-meta">
          {e.providerName || e.provider || ''}
          {e.description && <> · {e.description}</>}
          {multiPlatform && e.platformName && <span className="oc-model-picker-badge oc-model-picker-badge--platform" data-testid="model-platform-badge"><PlatformLogo platform={e.platform} />{e.platformName}</span>}
        </span>
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
            onToggleFavorite(e.provider, e.model, !e.isFavorite, e.platform || undefined);
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
      entries={context?.status ? [] : context?.tabs ? entries : visibleEntries}
      tabs={context?.tabs ?? tabs}
      status={context?.status}
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
      sidebar={reasoning ? sidebar || null : undefined}
      onLayoutKeyDown={sidebar ? handleReasoningKey : undefined}
      dialogClassName={reasoning ? 'oc-model-picker--reasoning' : undefined}
      keepOpenOnSelect={(model) => !!reasoning && optionsFor(model).length > 0}
      onSelect={(model) => {
        if (!reasoning || model !== currentModel || options.length === 0) onSelect(model);
        if (reasoning && optionsFor(model).length > 0) {
          requestAnimationFrame(() => currentLevelRef.current?.focus());
        }
      }}
      onClose={onClose}
      onBack={onBack}
    />
  );
}
