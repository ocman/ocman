import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { createPortal } from 'react-dom';
import Fuse, { type IFuseOptions } from 'fuse.js';
import '../CommandPalette.css';
import './ModelPicker.css';

// Every picker entry must expose a stable `value` (what gets passed to
// onSelect) plus whatever extra fields the concrete picker renders.
export interface PickerEntryBase {
  value: string;
}

// Flat list model: a section header or a selectable entry. Flat so
// keyboard navigation tracks a single index and headers are skipped.
type PickerItem<T> =
  | { kind: 'header'; label: string; key: string }
  | { kind: 'entry'; entry: T; key: string };

// groupIntoItems flattens entries into a header+row list. When
// showSections is false (i.e. under search) headers are dropped — Fuse
// returns results by relevance and per-section headers would shuffle
// that ordering. When showSections is true, entries are partitioned by
// sectionOf following sectionOrder; entries are emitted in input order
// within each section.
function groupIntoItems<T extends PickerEntryBase>(
  entries: T[],
  showSections: boolean,
  sectionOf: ((e: T) => string) | undefined,
  sectionOrder: string[] | undefined,
): PickerItem<T>[] {
  if (entries.length === 0) return [];
  const items: PickerItem<T>[] = [];

  if (!showSections || !sectionOf) {
    for (const e of entries) items.push({ kind: 'entry', entry: e, key: e.value });
    return items;
  }

  // Partition by section. When sectionOrder is given, emit sections in
  // that order; otherwise emit in first-seen order (matches the model
  // picker, whose entries arrive pre-sorted by section).
  if (sectionOrder && sectionOrder.length > 0) {
    const bySection: Record<string, T[]> = {};
    for (const e of entries) {
      const s = sectionOf(e);
      (bySection[s] ||= []).push(e);
    }
    for (const s of sectionOrder) {
      const list = bySection[s];
      if (!list || list.length === 0) continue;
      items.push({ kind: 'header', label: s, key: `h:${s}` });
      for (const e of list) items.push({ kind: 'entry', entry: e, key: e.value });
    }
    return items;
  }

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

export interface CommandListPickerProps<T extends PickerEntryBase> {
  open: boolean;
  /** Pre-built, pre-sorted entries to display. */
  entries: T[];
  /** Fuse field weights, e.g. [{ name: 'label', weight: 1 }]. */
  fuseKeys: NonNullable<IFuseOptions<T>['keys']>;
  /**
   * When provided AND there's no active query, entries are grouped into
   * sections by this function. Omit to always render a flat list.
   */
  sectionOf?: (e: T) => string;
  /** Optional explicit section ordering (otherwise first-seen order). */
  sectionOrder?: string[];
  /** Renders the inner content of one entry row (after the check column). */
  renderRow: (entry: T) => ReactNode;
  placeholder: (total: number) => string;
  emptyMessage: string;
  /** True when an entry should show the current-selection check mark. */
  isCurrent: (e: T) => boolean;
  initialQuery?: string;
  onSelect: (value: string) => void;
  onClose: () => void;
  /** Backspace-on-empty handler (model picker uses it to step back). */
  onBack?: () => void;
}

/**
 * CommandListPicker is the shared command-palette dropdown behind
 * ModelPicker and AgentPicker: a Fuse-backed, keyboard-navigable list
 * rendered in a portal over a backdrop. Concrete pickers supply the
 * entries, Fuse keys, sectioning, and per-row rendering; everything
 * else (query state, autofocus, extended search, header-skipping
 * keyboard nav, Escape/Backspace handling, the portal shell) lives here.
 */
export function CommandListPicker<T extends PickerEntryBase>({
  open,
  entries,
  fuseKeys,
  sectionOf,
  sectionOrder,
  renderRow,
  placeholder,
  emptyMessage,
  isCurrent,
  initialQuery,
  onSelect,
  onClose,
  onBack,
}: CommandListPickerProps<T>) {
  // Parent remounts on open (conditional render), so useState picks up
  // initialQuery fresh each invocation without resurrecting stale state.
  const [query, setQuery] = useState(initialQuery ?? '');
  const [selectedIndex, setSelectedIndex] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    requestAnimationFrame(() => inputRef.current?.focus());
  }, [open]);

  const fuse = useMemo(
    () => new Fuse(entries, {
      keys: fuseKeys,
      // 0.45 keeps one-typo queries surfacing without opening the floodgates.
      threshold: 0.45,
      // Don't penalize matches found late in long strings.
      ignoreLocation: true,
      // Friendlier for non-ASCII names.
      ignoreDiacritics: true,
      // AND-of-tokens so "claude opus" matches rows with both tokens.
      useExtendedSearch: true,
      includeScore: true,
    }),
    // fuseKeys is treated as stable per picker; entries drives rebuilds.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [entries],
  );

  // Normalize whitespace and short-circuit empty queries.
  const extendedQuery = useMemo(() => query.trim().split(/\s+/).filter(Boolean).join(' '), [query]);

  const filteredEntries = useMemo(() => {
    if (!extendedQuery) return entries;
    return fuse.search(extendedQuery, { limit: 200 }).map((r) => r.item);
  }, [extendedQuery, fuse, entries]);

  // Section only when not searching; on search show a flat ranked list.
  const items = useMemo(
    () => groupIntoItems(filteredEntries, !query.trim(), sectionOf, sectionOrder),
    [filteredEntries, query, sectionOf, sectionOrder],
  );

  // Precompute selectable (entry) indexes so arrow keys skip headers.
  const entryIndexes = useMemo(
    () => items.reduce<number[]>((acc, it, i) => {
      if (it.kind === 'entry') acc.push(i);
      return acc;
    }, []),
    [items],
  );

  // selectedIndex indexes into entryIndexes, so we never land on a header.
  const effectiveIndex = entryIndexes.length === 0
    ? 0
    : Math.min(Math.max(selectedIndex, 0), entryIndexes.length - 1);
  const activeItemIndex = entryIndexes[effectiveIndex] ?? -1;

  useEffect(() => {
    if (!listRef.current || activeItemIndex < 0) return;
    const item = listRef.current.children[activeItemIndex] as HTMLElement | undefined;
    item?.scrollIntoView({ block: 'nearest' });
  }, [activeItemIndex]);

  const pick = useCallback((value: string) => {
    onSelect(value);
    onClose();
  }, [onSelect, onClose]);

  // Close on Escape regardless of focus target.
  useEffect(() => {
    if (!open) return;
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault();
        onClose();
      }
    };
    window.addEventListener('keydown', onKeyDown, true);
    return () => window.removeEventListener('keydown', onKeyDown, true);
  }, [open, onClose]);

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
    } else if (e.key === 'Backspace' && !query && onBack) {
      e.preventDefault();
      onBack();
    }
  }, [entryIndexes.length, effectiveIndex, items, activeItemIndex, pick, onClose, onBack, query]);

  if (!open) return null;

  const total = entries.length;

  return createPortal(
    <div className="oc-cmd-backdrop" onClick={onClose}>
      <div className="oc-cmd-palette oc-model-picker" onClick={(e) => e.stopPropagation()}>
        <div className="oc-cmd-input-wrap">
          <i className="bi bi-search oc-cmd-search-icon" />
          <input
            ref={inputRef}
            className="oc-cmd-input"
            type="text"
            placeholder={placeholder(total)}
            value={query}
            onChange={(e) => { setQuery(e.target.value); setSelectedIndex(0); }}
            onKeyDown={onInputKeyDown}
          />
          <kbd className="oc-cmd-kbd">ESC</kbd>
        </div>
        <div className="oc-cmd-results" ref={listRef}>
          {filteredEntries.length === 0 && (
            <div className="oc-cmd-empty">{emptyMessage}</div>
          )}
          {items.map((it, i) => {
            if (it.kind === 'header') {
              return <div key={it.key} className="oc-model-picker-header">{it.label}</div>;
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
                  data-active={isCurrent(e) ? 'true' : 'false'}
                >
                  {isCurrent(e) ? <i className="bi bi-check2" /> : null}
                </span>
                {renderRow(e)}
              </div>
            );
          })}
        </div>
      </div>
    </div>,
    document.body,
  );
}
