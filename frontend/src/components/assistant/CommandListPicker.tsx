import { useCallback, useEffect, useId, useMemo, useRef, useState, type ReactNode } from 'react';
import { createPortal } from 'react-dom';
import { fuzzyMatch } from '../../lib/format';
import { Modal } from '../Modal';
import '../CommandPalette.css';
import './ModelPicker.css';

// Every picker entry must expose a stable `value` (what gets passed to
// onSelect) plus whatever extra fields the concrete picker renders.
export interface PickerEntryBase {
  value: string;
}

type SearchKey<T> = keyof T | { name: keyof T; weight?: number };

// Flat list model: a section header or a selectable entry. Flat so
// keyboard navigation tracks a single index and headers are skipped.
type PickerItem<T> =
  | { kind: 'header'; label: string; key: string }
  | { kind: 'entry'; entry: T; key: string };

// groupIntoItems flattens entries into a header+row list. When
  // showSections is false (i.e. under search) headers are dropped. When
  // showSections is true, entries are partitioned by
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
  /** Fields included in fuzzy matching. Unused when `searchable` is false. */
  fuseKeys?: SearchKey<T>[];
  /**
   * False renders a static title instead of the search input and hosts the
   * keyboard model on the listbox itself; the initially highlighted row is
   * the current entry. Default true.
   */
  searchable?: boolean;
  /** Extra class on the dialog (e.g. to narrow it). */
  dialogClassName?: string;
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
 * ModelPicker and AgentPicker: a fuzzy-filtered, keyboard-navigable list
 * rendered in a portal over a backdrop. Concrete pickers supply the
 * entries, search fields, sectioning, and per-row rendering; everything
 * else (query state, autofocus, extended search, header-skipping
 * keyboard nav, Escape/Backspace handling, the portal shell) lives here.
 */
export function CommandListPicker<T extends PickerEntryBase>({
  open,
  entries,
  fuseKeys,
  searchable = true,
  dialogClassName,
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
  const [query, setQuery] = useState(searchable ? (initialQuery ?? '') : '');
  // ponytail: the non-searching start index is computed over `entries`,
  // which only equals the rendered order when there are no sections.
  const [selectedIndex, setSelectedIndex] = useState(
    () => (searchable ? 0 : Math.max(0, entries.findIndex(isCurrent))),
  );
  const inputRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLDivElement>(null);
  // The list is a listbox driven from the input (combobox +
  // aria-activedescendant): focus stays in the search field, which is
  // what the arrow keys already assume. Without an input the listbox
  // itself takes focus and hosts the same key handler.
  const listId = useId();
  const optionId = (index: number) => `${listId}-option-${index}`;

  useEffect(() => {
    if (!open) return;
    requestAnimationFrame(() => (searchable ? inputRef.current : listRef.current)?.focus());
  }, [open, searchable]);

  // Normalize whitespace and short-circuit empty queries.
  const extendedQuery = useMemo(() => query.trim().split(/\s+/).filter(Boolean).join(' '), [query]);

  const filteredEntries = useMemo(() => {
    if (!extendedQuery) return entries;
    const keys = (fuseKeys ?? ['value']).map((key) => typeof key === 'object' ? key.name : key).filter((key): key is keyof T & string => typeof key === 'string');
    return entries.filter((entry) => fuzzyMatch(extendedQuery, keys.map((key) => String((entry as unknown as Record<string, unknown>)[key] ?? '')).join(' '))).slice(0, 200);
  }, [entries, extendedQuery, fuseKeys]);

  // Section only when not searching; on search show a flat filtered list.
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
    <Modal
      label={placeholder(total)}
      onClose={onClose}
      backdropClassName="oc-cmd-backdrop"
      dialogClassName={`oc-cmd-palette oc-model-picker${dialogClassName ? ` ${dialogClassName}` : ''}`}
    >
      <div className="oc-cmd-input-wrap">
        {searchable ? (
          <>
            <i className="bi bi-search oc-cmd-search-icon" />
            <input
              ref={inputRef}
              className="oc-cmd-input"
              type="text"
              role="combobox"
              aria-expanded="true"
              aria-autocomplete="list"
              aria-controls={listId}
              aria-activedescendant={activeItemIndex >= 0 ? optionId(activeItemIndex) : undefined}
              placeholder={placeholder(total)}
              value={query}
              onChange={(e) => { setQuery(e.target.value); setSelectedIndex(0); }}
              onKeyDown={onInputKeyDown}
            />
          </>
        ) : (
          <span className="oc-model-picker-title">{placeholder(total)}</span>
        )}
        <kbd className="oc-cmd-kbd">ESC</kbd>
      </div>
      <div
        className="oc-cmd-results"
        id={listId}
        role="listbox"
        aria-label={placeholder(total)}
        ref={listRef}
        tabIndex={searchable ? undefined : 0}
        aria-activedescendant={!searchable && activeItemIndex >= 0 ? optionId(activeItemIndex) : undefined}
        onKeyDown={searchable ? undefined : onInputKeyDown}
      >
        {filteredEntries.length === 0 && (
          <div className="oc-cmd-empty" role="presentation">{emptyMessage}</div>
        )}
        {items.map((it, i) => {
          if (it.kind === 'header') {
            return <div key={it.key} className="oc-model-picker-header" role="presentation">{it.label}</div>;
          }
          const e = it.entry;
          return (
            <div
              key={it.key}
              id={optionId(i)}
              role="option"
              aria-selected={i === activeItemIndex}
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
    </Modal>,
    document.body,
  );
}
