import { useMemo, useSyncExternalStore } from 'react';
import { randomId } from './randomId';

const DRAFTS_KEY = 'ocman.composerDrafts.v1';
const TEXT_PREFIX = DRAFTS_KEY + ':';
const CLEAR_PREFIX = 'ocman.composerDraftClear.v1:';
type TextEntry = { kind: 'ocman/composer-text'; id: string; text: string };

type Drafts = Record<string, string>;
const draftVersions = new Map<string, number>();
const VERSION_PREFIX = 'ocman.composerDraftRevision.v1:';
export function getDraftVersion(sessionId: string) {
  let stored = 0;
  try { stored = Number(window.localStorage.getItem(VERSION_PREFIX + sessionId)) || 0; } catch { /* Keep the live revision. */ }
  return Math.max(draftVersions.get(sessionId) || 0, stored);
}

/** Invalidate outstanding autosaves and failed-send recovery before clearing. */
export function discardDraft(sessionId: string, entryId = getDraftEntryId(sessionId)) {
  const version = getDraftVersion(sessionId) + 1;
  draftVersions.set(sessionId, version);
  clearDraft(sessionId, entryId);
  try { window.localStorage.setItem(VERSION_PREFIX + sessionId, String(version)); } catch { /* Keep the live tombstone. */ }
}

function loadDrafts(): Drafts {
  if (typeof window === 'undefined') return {};

  try {
    const raw = window.localStorage.getItem(DRAFTS_KEY);
    if (!raw) return {};
    const parsed = JSON.parse(raw) as Drafts;
    if (!parsed || typeof parsed !== 'object') return {};
    return parsed;
  } catch {
    return {};
  }
}

function readEntry(sessionId: string): TextEntry {
  const raw = window.localStorage.getItem(TEXT_PREFIX + sessionId) ?? loadDrafts()[sessionId] ?? '';
  try {
    const entry = JSON.parse(raw);
    if (entry?.kind === 'ocman/composer-text' && typeof entry.id === 'string' && typeof entry.text === 'string') return entry;
  } catch { /* The earlier per-draft values contain plain text. */ }
  return { kind: 'ocman/composer-text', id: 'legacy', text: typeof raw === 'string' ? raw : '' };
}

export const getDraftEntryId = (id: string) => { try { return readEntry(id).id; } catch { return 'legacy'; } };
export const getDraftClearId = (id: string) => { try { return localStorage.getItem(CLEAR_PREFIX + id); } catch { return null; } };

export function getDraft(sessionId: string): string {
  try { const entry = readEntry(sessionId); return getDraftClearId(sessionId) === entry.id ? '' : entry.text; }
  catch { return ''; }
}

export function saveDraft(sessionId: string, text: string, version = getDraftVersion(sessionId)) {
  if (version !== getDraftVersion(sessionId)) return;
  if (!text) { discardDraft(sessionId); return; }
  try { window.localStorage.setItem(TEXT_PREFIX + sessionId, JSON.stringify({ kind: 'ocman/composer-text', id: randomId(), text })); } catch { /* Best-effort autosave. */ }
  emit();
}

export function clearDraft(sessionId: string, entryId = getDraftEntryId(sessionId)) {
  // A clear owns one immutable edit identity. It cannot erase an intervening source save.
  try { window.localStorage.setItem(CLEAR_PREFIX + sessionId, entryId); } catch { /* Best-effort clear. */ }
  emit();
}

/** Copy before clearing: a failed write must leave the original recoverable. */
export function migrateDraft(from: string, to: string): boolean {
  const entryId = getDraftEntryId(from);
  const text = getDraft(from);
  if (!text) return true;
  try {
    window.localStorage.setItem(TEXT_PREFIX + to, JSON.stringify({ kind: 'ocman/composer-text', id: randomId(), text }));
    window.localStorage.setItem(CLEAR_PREFIX + from, entryId);
    emit();
    return getDraftEntryId(from) === entryId;
  } catch { return false; }
}

// --- which sessions have an unsent draft (sidebar indicator) ---
// ponytail: the snapshot is the sorted id list joined into a string so
// useSyncExternalStore gets a stable primitive without a cache layer.

const listeners = new Set<() => void>();
let snapshot: string | null = null;

function computeSnapshot() {
  const ids = new Set(Object.keys(loadDrafts()));
  try {
    for (let i = 0; i < localStorage.length; i++) {
      const key = localStorage.key(i);
      if (key?.startsWith(TEXT_PREFIX)) ids.add(key.slice(TEXT_PREFIX.length));
    }
  } catch { /* Legacy reads remain available when storage is blocked. */ }
  return [...ids].filter((id) => getDraft(id)).sort().join('\n');
}

function emit() {
  const next = computeSnapshot();
  if (next === snapshot) return;
  snapshot = next;
  for (const l of listeners) l();
}

export function subscribeDraftSessionIds(cb: () => void) {
  listeners.add(cb);
  // Another tab wrote drafts for the same user.
  const onStorage = (e: StorageEvent) => { if (e.key === null || e.key === DRAFTS_KEY || e.key.startsWith(TEXT_PREFIX) || e.key.startsWith(CLEAR_PREFIX)) emit(); };
  window.addEventListener('storage', onStorage);
  return () => {
    listeners.delete(cb);
    window.removeEventListener('storage', onStorage);
  };
}

function getSnapshot() {
  if (snapshot === null) snapshot = computeSnapshot();
  return snapshot;
}

/** Session ids that currently hold an unsent composer draft. */
export function useDraftSessionIds(): Set<string> {
  const key = useSyncExternalStore(subscribeDraftSessionIds, getSnapshot, () => '');
  return useMemo(() => new Set(key ? key.split('\n') : []), [key]);
}
