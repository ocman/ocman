import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import Fuse from 'fuse.js';
import type { AgentInfo } from '../../lib/api';
import { agentColor } from '../../lib/agentColor';
import '../CommandPalette.css';
import './ModelPicker.css';

export interface AgentPickerProps {
  open: boolean;
  // Known agent names (union of active + any hard-coded list the composer
  // uses). Always present so the picker works even before /agent returns.
  agentNames: string[];
  // Rich agent info from OpenCode's /agent endpoint. Optional — fields like
  // description and mode light up when available.
  agents?: AgentInfo[];
  currentAgent?: string;
  activeAgent?: string; // for the "Active" section + distinguishing current
  initialQuery?: string;
  onSelect: (agent: string) => void;
  onClose: () => void;
}

// Internal row shape used for rendering.
interface PickerEntry {
  value: string;         // agent name — what we send to onSelect
  label: string;
  description: string;
  mode: 'primary' | 'subagent' | 'all' | '';
  color: string;
  hidden: boolean;
  builtIn: boolean;
  isActive: boolean;     // matches activeAgent (session's current)
  isCurrent: boolean;    // matches currentAgent (selectedAgent || activeAgent)
}

type PickerItem =
  | { kind: 'header'; label: string; key: string }
  | { kind: 'entry'; entry: PickerEntry; key: string };

// buildEntries merges the known-name list with rich AgentInfo data. The
// AgentInfo side wins on fields, and we tag isActive/isCurrent so the UI can
// render section + selection state without another pass.
function buildEntries(
  agentNames: string[],
  agents: AgentInfo[] | undefined,
  activeAgent: string | undefined,
  currentAgent: string | undefined,
): PickerEntry[] {
  const byName = new Map<string, AgentInfo>();
  for (const a of agents || []) byName.set(a.name, a);

  // Union of names so we show known-but-unregistered agents (e.g. builtins
  // like "architect" the user has seen in other sessions) alongside the live
  // /agent catalog. De-dupe by name.
  const names = Array.from(new Set([...agentNames, ...(agents || []).map((a) => a.name)]));

  return names.map((name) => {
    const info = byName.get(name);
    return {
      value: name,
      label: name,
      description: info?.description ?? '',
      mode: info?.mode ?? '',
      color: agentColor(name, agents),
      hidden: !!info?.hidden,
      builtIn: !!info?.builtIn,
      isActive: !!activeAgent && name === activeAgent,
      isCurrent: !!currentAgent && name === currentAgent,
    };
  });
}

// groupIntoItems flattens entries into a header+row list. Under search we
// drop headers (matches the ModelPicker behavior — Fuse returns results by
// relevance, and per-section headers would shuffle that ordering).
function groupIntoItems(entries: PickerEntry[], showSections: boolean): PickerItem[] {
  if (entries.length === 0) return [];
  const items: PickerItem[] = [];

  if (!showSections) {
    for (const e of entries) items.push({ kind: 'entry', entry: e, key: e.value });
    return items;
  }

  // Sections mirror how OpenCode surfaces agents:
  //   Active  → the session's current agent (pinned at top)
  //   Primary → agents that can drive a conversation
  //   Subagents → agents invoked by other agents (task, research, etc.)
  //   Hidden → hidden: true entries, surfaced only in search
  const sectionOf = (e: PickerEntry): string => {
    if (e.isActive) return 'Active';
    if (e.hidden) return 'Hidden';
    if (e.mode === 'subagent') return 'Subagents';
    return 'Primary';
  };
  // Stable partition so each section respects the input order (which the
  // caller controls — typically alphabetical).
  const sectionOrder = ['Active', 'Primary', 'Subagents', 'Hidden'];
  const bySection: Record<string, PickerEntry[]> = {};
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

// Command-palette–style modal for picking an agent. Styled alongside
// ModelPicker so the two feel interchangeable.
export function AgentPicker({
  open,
  agentNames,
  agents,
  currentAgent,
  activeAgent,
  initialQuery,
  onSelect,
  onClose,
}: AgentPickerProps) {
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

  const entries = useMemo<PickerEntry[]>(
    () => buildEntries(agentNames, agents, activeAgent, currentAgent),
    [agentNames, agents, activeAgent, currentAgent],
  );

  // Weights mirror the model picker: agent name gets the most weight so
  // "arch" matches "architect" before it matches something with "arch" deep
  // in its description.
  const fuse = useMemo(
    () => new Fuse(entries, {
      keys: [
        { name: 'label', weight: 1.0 },
        { name: 'description', weight: 0.4 },
        { name: 'mode', weight: 0.2 },
      ],
      threshold: 0.45,
      ignoreLocation: true,
      ignoreDiacritics: true,
      useExtendedSearch: true,
      includeScore: true,
    }),
    [entries],
  );

  const extendedQuery = useMemo(() => query.trim().split(/\s+/).filter(Boolean).join(' '), [query]);

  const filteredEntries = useMemo(() => {
    if (!extendedQuery) return entries;
    return fuse.search(extendedQuery, { limit: 200 }).map((r) => r.item);
  }, [extendedQuery, fuse, entries]);

  const items = useMemo(
    () => groupIntoItems(filteredEntries, !query.trim()),
    [filteredEntries, query],
  );

  const entryIndexes = useMemo(
    () => items.reduce<number[]>((acc, it, i) => {
      if (it.kind === 'entry') acc.push(i);
      return acc;
    }, []),
    [items],
  );

  const effectiveIndex = entryIndexes.length === 0
    ? 0
    : Math.min(Math.max(selectedIndex, 0), entryIndexes.length - 1);
  const activeItemIndex = entryIndexes[effectiveIndex] ?? -1;

  useEffect(() => {
    if (!listRef.current || activeItemIndex < 0) return;
    const item = listRef.current.children[activeItemIndex] as HTMLElement | undefined;
    item?.scrollIntoView({ block: 'nearest' });
  }, [activeItemIndex]);

  const pick = useCallback((agent: string) => {
    onSelect(agent);
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

  const totalAgents = entries.length;

  return createPortal(
    <div className="oc-cmd-backdrop" onClick={onClose}>
      <div className="oc-cmd-palette oc-model-picker" onClick={(e) => e.stopPropagation()}>
        <div className="oc-cmd-input-wrap">
          <i className="bi bi-search oc-cmd-search-icon" />
          <input
            ref={inputRef}
            className="oc-cmd-input"
            type="text"
            placeholder={totalAgents > 0 ? `Select an agent (${totalAgents} available)...` : 'Select an agent...'}
            value={query}
            onChange={(e) => { setQuery(e.target.value); setSelectedIndex(0); }}
            onKeyDown={onInputKeyDown}
          />
          <kbd className="oc-cmd-kbd">ESC</kbd>
        </div>
        <div className="oc-cmd-results" ref={listRef}>
          {filteredEntries.length === 0 && (
            <div className="oc-cmd-empty">No agents found</div>
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
                  data-active={e.isCurrent ? 'true' : 'false'}
                >
                  {e.isCurrent ? <i className="bi bi-check2" /> : null}
                </span>
                <div className="oc-cmd-item-content">
                  <span className="oc-cmd-title">
                    <span
                      className="oc-agent-swatch"
                      aria-hidden="true"
                      style={{ background: e.color }}
                    />
                    {e.label}
                    {e.isActive && (
                      <span className="oc-model-picker-badge oc-model-picker-badge--star" title="Active in this session">
                        <i className="bi bi-star-fill" />
                      </span>
                    )}
                    {e.builtIn && (
                      <span className="oc-model-picker-badge oc-model-picker-badge--default" title="Built-in agent">
                        built-in
                      </span>
                    )}
                    {e.mode === 'subagent' && (
                      <span className="oc-model-picker-badge oc-model-picker-badge--archived" title="Subagent">
                        subagent
                      </span>
                    )}
                  </span>
                  {e.description && (
                    <span className="oc-cmd-meta">{e.description}</span>
                  )}
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
