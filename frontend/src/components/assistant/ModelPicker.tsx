import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import Fuse from 'fuse.js';
import type { SessionModelEntry } from '../../lib/api';
import '../CommandPalette.css';
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
  onClose: () => void;
}

// Internal row type used for rendering. Derived from either `modelEntries`
// (rich server-side merged data) or `models` (plain `provider/model` strings).
interface PickerEntry {
  value: string;             // "provider/model" — what we send to onSelect
  provider: string;
  providerName: string;      // human-readable, falls back to provider id
  model: string;
  modelName: string;         // human-readable, falls back to model id
  recentRank: number;        // 1-based recency position, 0 if not recent
  isSessionDefault: boolean;
  isProviderDefault: boolean;
  isAvailable: boolean;
  isCurrent: boolean;
}

// Rendered list items: either a provider group header or a model row. Flat
// so keyboard navigation only has to track a single index; headers are
// marked non-selectable.
type PickerItem =
  | { kind: 'header'; label: string; key: string }
  | { kind: 'entry'; entry: PickerEntry; key: string };

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
      isCurrent: !!currentModel && m === currentModel,
    };
  });
  entries.sort((a, b) => {
    if (a.provider !== b.provider) return a.provider.localeCompare(b.provider);
    return a.model.localeCompare(b.model);
  });
  return entries;
}

// groupIntoItems flattens entries into a header+row list for display.
//
// When `showSections` is true (rich data, no query), inserts section headers
// (Recent / Recommended / All models / Archived) to help the user navigate
// the full catalog. Under search, headers are dropped entirely — Fuse returns
// results ranked by relevance, so any provider-grouping would either
// reshuffle the ranking or produce duplicated headers (top 5 matches spread
// across 5 providers = 5 headers + 5 rows = 10 visible slots for 5 results).
// A flat ranked list matches how VS Code / Raycast render search results.
function groupIntoItems(entries: PickerEntry[], showSections: boolean): PickerItem[] {
  if (entries.length === 0) return [];
  const items: PickerItem[] = [];

  if (!showSections) {
    for (const e of entries) {
      items.push({ kind: 'entry', entry: e, key: e.value });
    }
    return items;
  }

  // Sections: Recent (session default + recents) → Recommended (provider
  // defaults) → All models (the rest of the available catalog) → Archived
  // (historical entries whose provider is no longer connected).
  const sectionOf = (e: PickerEntry): string => {
    if (e.isSessionDefault || e.recentRank > 0) return 'Recent';
    if (e.isProviderDefault) return 'Recommended';
    if (e.isAvailable) return 'All models';
    return 'Archived';
  };
  let currentSection = '';
  for (const e of entries) {
    const section = sectionOf(e);
    if (section !== currentSection) {
      currentSection = section;
      items.push({ kind: 'header', label: section, key: `h:${section}` });
    }
    items.push({ kind: 'entry', entry: e, key: e.value });
  }
  return items;
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
  onClose,
}: ModelPickerProps) {
  const useRich = !!(modelEntries && modelEntries.length > 0);

  // Parent remounts this component on each open (conditional render), so
  // `useState` initializers pick up the fresh `initialQuery` every time.
  const [query, setQuery] = useState(initialQuery ?? '');
  const [selectedIndex, setSelectedIndex] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    requestAnimationFrame(() => inputRef.current?.focus());
  }, [open]);

  const entries = useMemo<PickerEntry[]>(
    () => useRich ? buildEntriesFromRich(modelEntries!, currentModel) : buildEntriesFromStrings(models, currentModel),
    [useRich, modelEntries, models, currentModel],
  );

  // Field weighting: model display name and model id get the most weight
  // since that's what users type first ("opus", "sonnet"). Provider name/id
  // rank below so a query like "claude" doesn't push Anthropic's entire
  // catalog above a more specific "claude-opus" match. `value` is the full
  // provider/model string — useful for exact slash-separated matches but
  // would inflate any partial match, so it gets the lowest weight.
  const fuse = useMemo(
    () => new Fuse(entries, {
      keys: [
        { name: 'modelName', weight: 1.0 },
        { name: 'model', weight: 0.9 },
        { name: 'providerName', weight: 0.5 },
        { name: 'provider', weight: 0.5 },
        { name: 'value', weight: 0.2 },
      ],
      // 0.45 is a small bump from 0.4 so one-typo queries like "claud" still
      // surface "claude-*" entries without opening the floodgates.
      threshold: 0.45,
      // ignoreLocation: don't penalize matches found late in long strings,
      // so "opus-4" still ranks high inside "anthropic/claude-opus-4-7".
      ignoreLocation: true,
      // ignoreDiacritics: friendlier for non-ASCII model names.
      ignoreDiacritics: true,
      // useExtendedSearch: enables AND-of-tokens so "claude opus" matches
      // rows containing both tokens (order-independent). Each space-separated
      // token becomes its own implicit-AND clause.
      useExtendedSearch: true,
      includeScore: true,
    }),
    [entries],
  );

  // Split the query into tokens and AND them together via Fuse's extended
  // search syntax. A raw query "claude opus" becomes "claude opus" (already
  // AND-of-tokens under extended search), but we defensively normalize so
  // multiple spaces / tabs collapse and empty queries short-circuit.
  const extendedQuery = useMemo(() => query.trim().split(/\s+/).filter(Boolean).join(' '), [query]);

  const filteredEntries = useMemo(() => {
    if (!extendedQuery) return entries;
    return fuse.search(extendedQuery, { limit: 200 }).map((r) => r.item);
  }, [extendedQuery, fuse, entries]);

  // Preserve server-provided order only when we have rich data AND no query.
  // On search, fall back to alphabetical provider grouping so results read
  // coherently instead of jumping across sections.
  const items = useMemo(
    () => groupIntoItems(filteredEntries, useRich && !query.trim()),
    [filteredEntries, useRich, query],
  );

  // Precompute the indexes of selectable (entry) items so arrow keys skip
  // headers without having to iterate the whole list each press.
  const entryIndexes = useMemo(
    () => items.reduce<number[]>((acc, it, i) => {
      if (it.kind === 'entry') acc.push(i);
      return acc;
    }, []),
    [items],
  );

  // `selectedIndex` is an index into `entryIndexes`, not `items` — that way
  // we never land on a header.
  const effectiveIndex = entryIndexes.length === 0
    ? 0
    : Math.min(Math.max(selectedIndex, 0), entryIndexes.length - 1);
  const activeItemIndex = entryIndexes[effectiveIndex] ?? -1;

  useEffect(() => {
    if (!listRef.current || activeItemIndex < 0) return;
    const item = listRef.current.children[activeItemIndex] as HTMLElement | undefined;
    item?.scrollIntoView({ block: 'nearest' });
  }, [activeItemIndex]);

  const pick = useCallback((model: string) => {
    onSelect(model);
    onClose();
  }, [onSelect, onClose]);

  const onInputKeyDown = useCallback((e: React.KeyboardEvent) => {
    if (e.key === 'Escape') {
      e.preventDefault();
      onClose();
    } else if (e.key === 'ArrowDown') {
      e.preventDefault();
      setSelectedIndex(Math.min(effectiveIndex + 1, entryIndexes.length - 1));
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      setSelectedIndex(Math.max(effectiveIndex - 1, 0));
    } else if (e.key === 'Enter') {
      e.preventDefault();
      const picked = items[activeItemIndex];
      if (picked && picked.kind === 'entry') pick(picked.entry.value);
    }
  }, [entryIndexes.length, effectiveIndex, items, activeItemIndex, pick, onClose]);

  if (!open) return null;

  const totalModels = entries.length;
  const visibleModels = filteredEntries.length;

  return createPortal(
    <div className="oc-cmd-backdrop" onClick={onClose}>
      <div className="oc-cmd-palette oc-model-picker" onClick={(e) => e.stopPropagation()}>
        <div className="oc-cmd-input-wrap">
          <i className="bi bi-search oc-cmd-search-icon" />
          <input
            ref={inputRef}
            className="oc-cmd-input"
            type="text"
            placeholder={totalModels > 0 ? `Select a model (${totalModels} available)...` : 'Select a model...'}
            value={query}
            onChange={(e) => { setQuery(e.target.value); setSelectedIndex(0); }}
            onKeyDown={onInputKeyDown}
          />
          <kbd className="oc-cmd-kbd">ESC</kbd>
        </div>
        <div className="oc-cmd-results" ref={listRef}>
          {visibleModels === 0 && (
            <div className="oc-cmd-empty">No models found</div>
          )}
          {items.map((it, i) => {
            if (it.kind === 'header') {
              return (
                <div key={it.key} className="oc-model-picker-header">{it.label}</div>
              );
            }
            const e = it.entry;
            return (
              <div
                key={it.key}
                className={`oc-cmd-item oc-model-picker-row${i === activeItemIndex ? ' oc-cmd-item--selected' : ''}`}
                onClick={() => pick(e.value)}
                onMouseEnter={() => {
                  const idx = entryIndexes.indexOf(i);
                  if (idx >= 0) setSelectedIndex(idx);
                }}
              >
                <span
                  className="oc-model-picker-check"
                  aria-hidden="true"
                  data-active={e.isCurrent ? 'true' : 'false'}
                >
                  {e.isCurrent ? <i className="bi bi-check2" /> : null}
                </span>
                <div className="oc-cmd-item-content">
                  <span className="oc-cmd-title">
                    {e.modelName}
                    {e.isSessionDefault && (
                      <span className="oc-model-picker-badge oc-model-picker-badge--star" title="Session default">
                        <i className="bi bi-star-fill" />
                      </span>
                    )}
                    {!e.isSessionDefault && e.recentRank > 0 && (
                      <span
                        className="oc-model-picker-badge oc-model-picker-badge--used"
                        title="Recently used"
                      >
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
                  <span className="oc-cmd-meta">
                    {e.providerName || e.provider || ''}
                  </span>
                </div>
              </div>
            );
          })}
        </div>
      </div>
    </div>,
    document.body,
  );
}
