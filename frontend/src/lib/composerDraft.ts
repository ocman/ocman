import { useMemo, useSyncExternalStore } from 'react';
import { randomId } from './randomId';

const DRAFTS_KEY = 'ocman.composerDrafts.v1';
const TEXT_PREFIX = DRAFTS_KEY + ':';
const CLEAR_PREFIX = 'ocman.composerDraftClear.v1:';
const HEAD_PREFIX = 'ocman.composerDraftHead.v1:';
const VALUE_PREFIX = 'ocman.composerDraftText.v1:';
const ownerKey = (id: string, entry: string) => `${CLEAR_PREFIX}${encodeURIComponent(id)}:${encodeURIComponent(entry)}`;
const valueKey = (id: string, entry: string) => `${VALUE_PREFIX}${encodeURIComponent(id)}:${encodeURIComponent(entry)}`;
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
  const head = localStorage.getItem(HEAD_PREFIX + sessionId);
  if (head) return { kind: 'ocman/composer-text', id: head, text: localStorage.getItem(valueKey(sessionId, head)) || '' };
  const raw = window.localStorage.getItem(TEXT_PREFIX + sessionId) ?? loadDrafts()[sessionId] ?? '';
  try {
    const entry = JSON.parse(raw);
    if (entry?.kind === 'ocman/composer-text' && typeof entry.id === 'string' && typeof entry.text === 'string') return entry;
  } catch { /* The earlier per-draft values contain plain text. */ }
  return { kind: 'ocman/composer-text', id: raw ? 'legacy' : localStorage.getItem(CLEAR_PREFIX + sessionId) || 'legacy', text: typeof raw === 'string' ? raw : '' };
}

export const getDraftEntryId = (id: string) => { try { return readEntry(id).id; } catch { return 'legacy'; } };
export const getDraftClearId = (id: string) => {
  try {
    const head = localStorage.getItem(HEAD_PREFIX + id);
    // A reclaimed body without a tombstone (quota fallback) is cleared as well.
    return head && (localStorage.getItem(ownerKey(id, head)) || localStorage.getItem(valueKey(id, head)) === null) ? head : localStorage.getItem(CLEAR_PREFIX + id);
  }
  catch { return null; }
};

export function getDraft(sessionId: string): string {
  try { const entry = readEntry(sessionId); return localStorage.getItem(ownerKey(sessionId, entry.id)) || getDraftClearId(sessionId) === entry.id ? '' : entry.text; }
  catch { return ''; }
}

export function saveDraft(sessionId: string, text: string, version = getDraftVersion(sessionId)) {
  if (version !== getDraftVersion(sessionId)) return;
  if (!text) { discardDraft(sessionId); return; }
  try { writeEntry(sessionId, text); } catch { /* Best-effort autosave. */ }
  emit();
}

export function clearDraft(sessionId: string, entryId = getDraftEntryId(sessionId)) {
  try { clearEntry(sessionId, entryId); } catch { /* Best-effort clear. */ }
  emit();
}

/** Copy before clearing: a failed write must leave the original recoverable. */
export function migrateDraft(from: string, to: string): boolean {
  const entryId = getDraftEntryId(from);
  const text = getDraft(from);
  if (!text) return true;
  try {
    writeEntry(to, text);
    clearEntry(from, entryId);
    emit();
    return getDraftEntryId(from) === entryId;
  } catch { return false; }
}

function pruneLegacyDrafts() {
  const drafts = loadDrafts();
  let changed = false;
  for (const id of Object.keys(drafts)) {
    if (localStorage.getItem(ownerKey(id, 'legacy'))) { delete drafts[id]; changed = true; }
  }
  if (changed) {
    if (Object.keys(drafts).length) localStorage.setItem(DRAFTS_KEY, JSON.stringify(drafts));
    else localStorage.removeItem(DRAFTS_KEY);
  }
}

function writeEntry(id: string, text: string) {
  const previous = localStorage.getItem(HEAD_PREFIX + id);
  const entry = randomId();
  localStorage.setItem(valueKey(id, entry), text);
  try { localStorage.setItem(HEAD_PREFIX + id, entry); }
  catch (error) { try { localStorage.removeItem(valueKey(id, entry)); } catch { /* Preserve the previous head. */ } throw error; }
  try {
    if (previous) { localStorage.removeItem(valueKey(id, previous)); localStorage.removeItem(ownerKey(id, previous)); }
    localStorage.removeItem(TEXT_PREFIX + id); // New writers never use this legacy namespace.
    localStorage.setItem(ownerKey(id, 'legacy'), '1');
    pruneLegacyDrafts();
  } catch { /* Cleanup is retryable; it cannot undo the committed head. */ }
}

function clearEntry(id: string, entry: string) {
  // Monotonic per-edit tombstones cannot be reversed by a delayed older clear.
  try { localStorage.setItem(ownerKey(id, entry), '1'); }
  catch (error) {
    // Quota fallback: deleting frees space and only touches this exact immutable edit.
    if (entry !== 'legacy') { localStorage.removeItem(valueKey(id, entry)); return; }
    if (localStorage.getItem(HEAD_PREFIX + id)) throw error;
    localStorage.removeItem(TEXT_PREFIX + id);
    const drafts = loadDrafts();
    delete drafts[id];
    localStorage.setItem(DRAFTS_KEY, JSON.stringify(drafts)); // A smaller value fits under quota.
    return;
  }
  try {
    localStorage.removeItem(valueKey(id, entry));
    localStorage.setItem(CLEAR_PREFIX + id, entry);
    localStorage.setItem(ownerKey(id, 'legacy'), '1');
    localStorage.removeItem(TEXT_PREFIX + id);
    pruneLegacyDrafts();
  } catch { /* The logical clear remains durable if physical cleanup is interrupted. */ }
}

if (typeof window !== 'undefined') window.addEventListener('storage', (event) => {
  if (event.key === DRAFTS_KEY) try { pruneLegacyDrafts(); } catch { /* Retry cleanup on the next mutation. */ }
});

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
      if (key?.startsWith(HEAD_PREFIX)) ids.add(key.slice(HEAD_PREFIX.length));
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
  const onStorage = (e: StorageEvent) => { if (e.key === null || e.key === DRAFTS_KEY || e.key.startsWith(TEXT_PREFIX) || e.key.startsWith(CLEAR_PREFIX) || e.key.startsWith(HEAD_PREFIX)) emit(); };
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
